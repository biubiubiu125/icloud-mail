package protocol

import "testing"

func TestWebClientDoesNotRefillEmptyProxy(t *testing.T) {
	session, ok := iCloudWebSessionForClient(ICloudSession{
		Cookies: []SessionCookie{{Name: "X-APPLE-WEBAUTH-TOKEN", Value: "token", Domain: "icloud.com"}},
		LoginStates: []LoginState{{
			Kind:     LoginStateICloudWeb,
			ProxyURL: "socks5://stale.example:1080",
			Cookies:  []SessionCookie{{Name: "X-APPLE-WEBAUTH-TOKEN", Value: "token", Domain: "icloud.com"}},
		}},
	})
	if !ok {
		t.Fatal("web session was not recognized")
	}
	if session.ProxyURL != "" {
		t.Fatalf("empty session proxy was refilled from login state: %q", session.ProxyURL)
	}
}

func TestAppleAccountClientUsesSessionProxyOnly(t *testing.T) {
	state, ok := appleAccountLoginState(ICloudSession{
		ProxyURL: "http://account.example:8080",
		LoginStates: []LoginState{{
			Kind:     LoginStateAppleAccount,
			APIKey:   "key",
			Scnt:     "scnt",
			ProxyURL: "socks5://stale.example:1080",
		}},
	})
	if !ok {
		t.Fatal("apple account state was not recognized")
	}
	if state.ProxyURL != "http://account.example:8080" {
		t.Fatalf("state proxy = %q", state.ProxyURL)
	}

	cleared, ok := appleAccountLoginState(ICloudSession{
		LoginStates: []LoginState{{
			Kind:     LoginStateAppleAccount,
			APIKey:   "key",
			Scnt:     "scnt",
			ProxyURL: "socks5://stale.example:1080",
		}},
	})
	if !ok {
		t.Fatal("cleared session was not recognized")
	}
	if cleared.ProxyURL != "" {
		t.Fatalf("empty session proxy was refilled: %q", cleared.ProxyURL)
	}
}

func TestAppleAccountStateWriteDoesNotRefillSessionProxy(t *testing.T) {
	session := withAppleAccountLoginState(ICloudSession{}, LoginState{
		Kind:     LoginStateAppleAccount,
		APIKey:   "key",
		ProxyURL: "socks5://stale.example:1080",
	})
	if session.ProxyURL != "" {
		t.Fatalf("session proxy = %q", session.ProxyURL)
	}
	if len(session.LoginStates) != 1 || session.LoginStates[0].ProxyURL != "" {
		t.Fatalf("stored state proxy = %#v", session.LoginStates)
	}
}
