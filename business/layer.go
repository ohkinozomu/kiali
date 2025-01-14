package business

import (
	"context"
	"sync"

	"k8s.io/client-go/tools/clientcmd/api"

	"github.com/kiali/kiali/config"
	"github.com/kiali/kiali/jaeger"
	"github.com/kiali/kiali/kubernetes"
	"github.com/kiali/kiali/kubernetes/cache"
	"github.com/kiali/kiali/log"
	"github.com/kiali/kiali/prometheus"
)

// Layer is a container for fast access to inner services.
// A business layer is created per token/user. Any data that
// needs to be saved across layers is saved in the Kiali Cache.
type Layer struct {
	App            AppService
	Health         HealthService
	IstioConfig    IstioConfigService
	IstioStatus    IstioStatusService
	IstioCerts     IstioCertsService
	Jaeger         JaegerService
	k8sClients     map[string]kubernetes.ClientInterface // Key is the cluster name
	Mesh           MeshService
	Namespace      NamespaceService
	OpenshiftOAuth OpenshiftOAuthService
	ProxyLogging   ProxyLoggingService
	ProxyStatus    ProxyStatusService
	RegistryStatus RegistryStatusService
	Svc            SvcService
	TLS            TLSService
	TokenReview    TokenReviewService
	Validations    IstioValidationsService
	Workload       WorkloadService
}

// Global clientfactory and prometheus clients.
var (
	clientFactory    kubernetes.ClientFactory
	jaegerClient     jaeger.ClientInterface
	kialiCache       cache.KialiCache
	once             sync.Once
	prometheusClient prometheus.ClientInterface
)

// sets the global kiali cache var.
func initKialiCache() {
	if excludedWorkloads == nil {
		excludedWorkloads = make(map[string]bool)
		for _, w := range config.Get().KubernetesConfig.ExcludeWorkloads {
			excludedWorkloads[w] = true
		}
	}

	userClient, err := kubernetes.GetClientFactory()
	if err != nil {
		log.Errorf("Failed to create client factory. Err: %s", err)
		return
	}
	clientFactory = userClient

	// TODO: Remove conditonal once cache is fully mandatory.
	if config.Get().KubernetesConfig.CacheEnabled {
		log.Infof("Initializing Kiali Cache")

		// Initial list of namespaces to seed the cache with.
		// This is only necessary if the cache is namespace-scoped.
		// For a cluster-scoped cache, all namespaces are accessible.
		// TODO: This is leaking cluster-scoped vs. namespace-scoped in a way.
		var namespaceSeedList []string
		if !config.Get().AllNamespacesAccessible() {
			SAClients := clientFactory.GetSAClients()
			// Special case when using the SA as the user, to fetch all the namespaces initially
			initNamespaceService := NewNamespaceService(SAClients, SAClients)
			nss, err := initNamespaceService.GetNamespaces(context.Background())
			if err != nil {
				log.Errorf("Error fetching initial namespaces for populating the Kiali Cache. Details: %s", err)
				return
			}

			for _, ns := range nss {
				namespaceSeedList = append(namespaceSeedList, ns.Name)
			}
		}

		cache, err := cache.NewKialiCache(clientFactory, *config.Get(), namespaceSeedList...)
		if err != nil {
			log.Errorf("Error initializing Kiali Cache. Details: %s", err)
			return
		}

		kialiCache = cache
	}
}

func IsNamespaceCached(namespace string) bool {
	ok := kialiCache != nil && kialiCache.CheckNamespace(namespace)
	return ok
}

func IsResourceCached(namespace string, resource string) bool {
	ok := IsNamespaceCached(namespace)
	if ok && resource != "" {
		ok = kialiCache.CheckIstioResource(resource)
	}
	return ok
}

func Start() {
	// Kiali Cache will be initialized once at start up.
	once.Do(initKialiCache)
}

// Get the business.Layer
func Get(authInfo *api.AuthInfo) (*Layer, error) {
	// Creates new k8s clients based on the current users token
	userClients, err := clientFactory.GetClients(authInfo)
	if err != nil {
		return nil, err
	}

	// Use an existing Prometheus client if it exists, otherwise create and use in the future
	if prometheusClient == nil {
		prom, err := prometheus.NewClient()
		if err != nil {
			prometheusClient = nil
			return nil, err
		}
		prometheusClient = prom
	}

	// Create Jaeger client
	jaegerLoader := func() (jaeger.ClientInterface, error) {
		var err error
		if jaegerClient == nil {
			jaegerClient, err = jaeger.NewClient(authInfo.Token)
			if err != nil {
				jaegerClient = nil
			}
		}
		return jaegerClient, err
	}

	kialiSAClient := clientFactory.GetSAClients()
	return NewWithBackends(userClients, kialiSAClient, prometheusClient, jaegerLoader), nil
}

// SetWithBackends allows for specifying the ClientFactory and Prometheus clients to be used.
// Mock friendly. Used only with tests.
func SetWithBackends(cf kubernetes.ClientFactory, prom prometheus.ClientInterface) {
	clientFactory = cf
	prometheusClient = prom
}

// NewWithBackends creates the business layer using the passed k8sClients and prom clients.
// Note that the client passed here should *not* be the Kiali ServiceAccount client.
// It should be the user client based on the logged in user's token.
func NewWithBackends(userClients map[string]kubernetes.ClientInterface, kialiSAClients map[string]kubernetes.ClientInterface, prom prometheus.ClientInterface, jaegerClient JaegerLoader) *Layer {
	temporaryLayer := &Layer{}
	// TODO: Modify the k8s argument to other services to pass the whole k8s map if needed
	temporaryLayer.App = AppService{prom: prom, k8s: userClients[kubernetes.HomeClusterName], businessLayer: temporaryLayer}
	temporaryLayer.Health = HealthService{prom: prom, businessLayer: temporaryLayer}
	temporaryLayer.IstioConfig = IstioConfigService{k8s: userClients[kubernetes.HomeClusterName], businessLayer: temporaryLayer}
	temporaryLayer.IstioStatus = IstioStatusService{k8s: userClients[kubernetes.HomeClusterName], businessLayer: temporaryLayer}
	temporaryLayer.IstioCerts = IstioCertsService{k8s: userClients[kubernetes.HomeClusterName], businessLayer: temporaryLayer}
	temporaryLayer.Jaeger = JaegerService{loader: jaegerClient, businessLayer: temporaryLayer}
	temporaryLayer.k8sClients = userClients
	temporaryLayer.Mesh = NewMeshService(userClients[kubernetes.HomeClusterName], temporaryLayer, nil)
	temporaryLayer.Namespace = NewNamespaceService(userClients, kialiSAClients)
	temporaryLayer.OpenshiftOAuth = OpenshiftOAuthService{k8s: userClients[kubernetes.HomeClusterName]}
	temporaryLayer.ProxyStatus = ProxyStatusService{k8s: userClients[kubernetes.HomeClusterName], businessLayer: temporaryLayer}
	// Out of order because it relies on ProxyStatus
	temporaryLayer.ProxyLogging = ProxyLoggingService{k8s: userClients[kubernetes.HomeClusterName], proxyStatus: &temporaryLayer.ProxyStatus}
	temporaryLayer.RegistryStatus = RegistryStatusService{k8s: userClients[kubernetes.HomeClusterName], businessLayer: temporaryLayer}
	temporaryLayer.Svc = SvcService{prom: prom, k8s: userClients[kubernetes.HomeClusterName], businessLayer: temporaryLayer}
	temporaryLayer.TLS = TLSService{k8s: userClients[kubernetes.HomeClusterName], businessLayer: temporaryLayer}
	temporaryLayer.TokenReview = NewTokenReview(userClients[kubernetes.HomeClusterName])
	temporaryLayer.Validations = IstioValidationsService{k8s: userClients[kubernetes.HomeClusterName], businessLayer: temporaryLayer}

	// TODO: Remove conditional once cache is fully mandatory.
	if config.Get().KubernetesConfig.CacheEnabled {
		// The caching client effectively uses two different SA account tokens.
		// The kiali SA token is used for all cache methods. The cache methods are
		// read-only. Methods that are not cached and methods that modify objects
		// use the user's token through the normal client.
		// TODO: Always pass caching client once caching is mandatory.
		// TODO: Multicluster
		cachingClient := cache.NewCachingClient(kialiCache, userClients[kubernetes.HomeClusterName])
		temporaryLayer.Workload = *NewWorkloadService(cachingClient, prom, kialiCache, temporaryLayer, config.Get())
	} else {
		temporaryLayer.Workload = *NewWorkloadService(userClients[kubernetes.HomeClusterName], prom, kialiCache, temporaryLayer, config.Get())
	}

	return temporaryLayer
}

func Stop() {
	if kialiCache != nil {
		kialiCache.Stop()
	}
}
