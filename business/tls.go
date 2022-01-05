package business

import (
	networking_v1alpha3 "istio.io/client-go/pkg/apis/networking/v1alpha3"
	security_v1beta1 "istio.io/client-go/pkg/apis/security/v1beta1"
	core_v1 "k8s.io/api/core/v1"

	"github.com/kiali/kiali/config"
	"github.com/kiali/kiali/kubernetes"
	"github.com/kiali/kiali/models"
	"github.com/kiali/kiali/util/mtls"
)

type TLSService struct {
	k8s             kubernetes.ClientInterface
	businessLayer   *Layer
	enabledAutoMtls *bool
}

const (
	MTLSEnabled          = "MTLS_ENABLED"
	MTLSPartiallyEnabled = "MTLS_PARTIALLY_ENABLED"
	MTLSNotEnabled       = "MTLS_NOT_ENABLED"
	MTLSDisabled         = "MTLS_DISABLED"
)

func (in *TLSService) MeshWidemTLSStatus(namespaces []string) (models.MTLSStatus, error) {
	criteria := IstioConfigCriteria{
		AllNamespaces:              true,
		IncludeDestinationRules:    true,
		IncludePeerAuthentications: true,
	}
	istioConfigList, err := in.businessLayer.IstioConfig.GetIstioConfigList(criteria)
	if err != nil {
		return models.MTLSStatus{}, err
	}

	pas := kubernetes.FilterPeerAuthenticationByNamespace(config.Get().ExternalServices.Istio.RootNamespace, istioConfigList.PeerAuthentications)
	drs := kubernetes.FilterDestinationRulesByNamespaces(namespaces, istioConfigList.DestinationRules)

	mtlsStatus := mtls.MtlsStatus{
		PeerAuthentications: pas,
		DestinationRules:    drs,
		AutoMtlsEnabled:     in.hasAutoMTLSEnabled(),
		AllowPermissive:     false,
	}

	return models.MTLSStatus{
		Status: mtlsStatus.MeshMtlsStatus().OverallStatus,
	}, nil
}

func (in TLSService) NamespaceWidemTLSStatus(namespace string) (models.MTLSStatus, error) {
	nss, err := in.getNamespaces()
	if err != nil {
		return models.MTLSStatus{}, nil
	}

	criteria := IstioConfigCriteria{
		AllNamespaces:              true,
		IncludeDestinationRules:    true,
		IncludePeerAuthentications: true,
	}
	istioConfigList, err2 := in.businessLayer.IstioConfig.GetIstioConfigList(criteria)
	if err2 != nil {
		return models.MTLSStatus{}, err2
	}

	pas := kubernetes.FilterPeerAuthenticationByNamespace(namespace, istioConfigList.PeerAuthentications)
	if config.IsRootNamespace(namespace) {
		pas = []security_v1beta1.PeerAuthentication{}
	}
	drs := kubernetes.FilterDestinationRulesByNamespaces(nss, istioConfigList.DestinationRules)

	mtlsStatus := mtls.MtlsStatus{
		Namespace:           namespace,
		PeerAuthentications: pas,
		DestinationRules:    drs,
		AutoMtlsEnabled:     in.hasAutoMTLSEnabled(),
		AllowPermissive:     false,
	}

	return models.MTLSStatus{
		Status: mtlsStatus.NamespaceMtlsStatus().OverallStatus,
	}, nil
}

// TODO refactor business/istio_validations.go
func (in *TLSService) getAllDestinationRules(namespaces []string) ([]networking_v1alpha3.DestinationRule, error) {
	criteria := IstioConfigCriteria{
		AllNamespaces:           true,
		IncludeDestinationRules: true,
	}

	istioConfigList, err := in.businessLayer.IstioConfig.GetIstioConfigList(criteria)
	if err != nil {
		return nil, err
	}

	allDestinationRules := make([]networking_v1alpha3.DestinationRule, 0)
	for _, dr := range istioConfigList.DestinationRules {
		found := false
		for _, ns := range namespaces {
			if dr.Namespace == ns {
				found = true
				break
			}
		}
		if found {
			allDestinationRules = append(allDestinationRules, dr)
		}
	}
	return allDestinationRules, nil
}

func (in TLSService) getNamespaces() ([]string, error) {
	nss, nssErr := in.businessLayer.Namespace.GetNamespaces()
	if nssErr != nil {
		return nil, nssErr
	}

	nsNames := make([]string, 0)
	for _, ns := range nss {
		nsNames = append(nsNames, ns.Name)
	}
	return nsNames, nil
}

func (in *TLSService) hasAutoMTLSEnabled() bool {
	if in.enabledAutoMtls != nil {
		return *in.enabledAutoMtls
	}

	cfg := config.Get()
	var istioConfig *core_v1.ConfigMap
	var err error
	if IsNamespaceCached(cfg.IstioNamespace) {
		istioConfig, err = kialiCache.GetConfigMap(cfg.IstioNamespace, cfg.ExternalServices.Istio.ConfigMapName)
	} else {
		istioConfig, err = in.k8s.GetConfigMap(cfg.IstioNamespace, cfg.ExternalServices.Istio.ConfigMapName)
	}
	if err != nil {
		return true
	}
	mc, err := kubernetes.GetIstioConfigMap(istioConfig)
	if err != nil {
		return true
	}
	autoMtls := mc.GetEnableAutoMtls()
	in.enabledAutoMtls = &autoMtls
	return autoMtls
}
