import * as API from './Api';
import moment from 'moment';

import { LoginSession, KialiAppState } from '../store/Store';
import { AuthStrategy, AuthResult, AuthConfig } from '../types/Auth';
import { TimeInMilliseconds } from '../types/Common';
import { KialiDispatch } from '../types/Redux';
import authenticationConfig from '../config/AuthenticationConfig';

export interface LoginResult {
  status: AuthResult;
  session?: LoginSession;
  error?: any;
}

interface LoginStrategy<T extends unknown> {
  prepare: (info: AuthConfig) => Promise<AuthResult>;
  perform: (request: DispatchRequest<T>) => Promise<LoginResult>;
}

interface DispatchRequest<T> {
  dispatch: KialiDispatch;
  state: KialiAppState;
  data: T;
}

type NullDispatch = DispatchRequest<unknown>;

class AnonymousLogin implements LoginStrategy<unknown> {
  public async prepare(_info: AuthConfig) {
    return AuthResult.CONTINUE;
  }

  public async perform(_request: NullDispatch): Promise<LoginResult> {
    return {
      status: AuthResult.FAILURE,
      session: {
        username: API.ANONYMOUS_USER,
        expiresOn: moment().add(1, 'd').toISOString()
      }
    };
  }
}

interface WebLoginData {
  username: string;
  password: string;
}

class TokenLogin implements LoginStrategy<WebLoginData> {
  public async prepare(_info: AuthConfig) {
    return AuthResult.CONTINUE;
  }

  public async perform(request: DispatchRequest<WebLoginData>): Promise<LoginResult> {
    const session = (await API.login({ username: '', password: '', token: request.data.password })).data;

    return {
      status: AuthResult.SUCCESS,
      session: session
    };
  }
}

class HeaderLogin implements LoginStrategy<WebLoginData> {
  public async prepare(_info: AuthConfig) {
    return AuthResult.CONTINUE;
  }

  public async perform(_request: NullDispatch): Promise<LoginResult> {
    const session = (await API.login({ username: '', password: '', token: '' })).data;

    return {
      status: AuthResult.SUCCESS,
      session: session
    };
  }
}

class OAuthLogin implements LoginStrategy<unknown> {
  public async prepare(info: AuthConfig) {
    if (!info.authorizationEndpoint) {
      return AuthResult.FAILURE;
    }

    const pattern = /[#&](access_token|id_token)=/;
    if (pattern.test(window.location.hash)) {
      return AuthResult.CONTINUE;
    } else {
      return AuthResult.HOLD;
    }
  }

  public async perform(_request: NullDispatch): Promise<LoginResult> {
    // get the data from the url that was passed by the OAuth login.
    const session = (await API.checkOpenshiftAuth(window.location.hash.substring(1))).data;

    // remove the data that was passed by the OAuth login. In certain error situations this can cause the
    // page to enter a refresh loop since it tries to reload the page which then tries to reuse the bad token again.
    window.history.replaceState('', document.title, window.location.pathname + window.location.search);

    return {
      status: AuthResult.SUCCESS,
      session: session
    };
  }
}

export class LoginDispatcher {
  strategyMapping: Map<AuthStrategy, LoginStrategy<any>>;
  info?: AuthConfig;

  constructor() {
    this.strategyMapping = new Map();

    this.strategyMapping.set(AuthStrategy.anonymous, new AnonymousLogin());
    this.strategyMapping.set(AuthStrategy.openshift, new OAuthLogin());
    this.strategyMapping.set(AuthStrategy.token, new TokenLogin());
    this.strategyMapping.set(AuthStrategy.openid, new OAuthLogin());
    this.strategyMapping.set(AuthStrategy.header, new HeaderLogin());
  }

  public async prepare(): Promise<AuthResult> {
    const info = authenticationConfig;
    const strategy = this.strategyMapping.get(info.strategy)!;

    try {
      const delay = async (ms: TimeInMilliseconds = 3000) => {
        return new Promise(resolve => setTimeout(resolve, ms));
      };

      const result = await strategy.prepare(info);

      // If preparation requires a hold time, with things such as redirects that
      // require the auth flow to stop running for a while, we do that.
      //
      // If it fails to run for a while, we return a failure state.
      // This assume that the user is leaving the page for auth, which should be
      // the case for oauth implementations.
      if (result === AuthResult.HOLD) {
        await delay();

        return Promise.reject({
          status: AuthResult.FAILURE,
          error: 'Failed to redirect user to authentication page.'
        });
      } else {
        return result;
      }
    } catch (error) {
      return Promise.reject({ status: AuthResult.FAILURE, error });
    }
  }

  public async perform(request: DispatchRequest<any>): Promise<LoginResult> {
    const strategy = this.strategyMapping.get(authenticationConfig.strategy)!;

    try {
      return await strategy.perform(request);
    } catch (error) {
      return Promise.reject({ status: AuthResult.FAILURE, error });
    }
  }
}
