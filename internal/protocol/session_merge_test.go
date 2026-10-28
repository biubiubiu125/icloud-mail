package protocol

import "testing"

func TestMergeSavedSessionDoesNotTakeProxyFromIncoming(t *testing.T) {
	existing := ICloudSession{AppleID: "a@icloud.com", ProxyURL: "http://old.example:8080", LoginStates: []LoginState{{
		Kind: LoginStateICloudIMAP, IMAPEmail: "a@icloud.com", IMAPAppPassword: "abcd-efgh-ijkl-mnop",
	}}}
	incoming := ICloudSession{ProxyURL: "socks5://stale.example:1080", LoginStates: []LoginState{{
		Kind: LoginStateAppleAccount, APIKey: "key",
	}}}
	merged := MergeSavedSession(existing, incoming, "")
	if merged.ProxyURL != "" {
		t.Fatalf("empty account proxy must clear session proxy, got %q", merged.ProxyURL)
	}
	for _, state := range merged.LoginStates {
		if state.ProxyURL != "" {
			t.Fatalf("login state proxy = %q", state.ProxyURL)
		}
	}
	kept := false
	for _, state := range merged.LoginStates {
		if state.Kind == LoginStateICloudIMAP && state.IMAPAppPassword == "abcd-efgh-ijkl-mnop" {
			kept = true
		}
	}
	if !kept {
		t.Fatal("imap login was dropped")
	}
}

func TestSessionAccountMismatch(t *testing.T) {
	if protocolMismatch := SessionAccountMismatch("acc-1", "acc-2"); !protocolMismatch {
		t.Fatal("different account ids must not merge")
	}
	if SessionAccountMismatch("acc-1", "") {
		t.Fatal("empty incoming account id is a historical session")
	}
	if SessionAccountMismatch("acc-1", "acc-1") {
		t.Fatal("same account id is not a mismatch")
	}
}

func TestApplyAccountProxyClearsStale(t *testing.T) {
	session := ApplyAccountProxy(ICloudSession{ProxyURL: "socks5://stale.example:1080", LoginStates: []LoginState{{Kind: LoginStateICloudWeb, ProxyURL: "socks5://stale.example:1080"}}}, "")
	if session.ProxyURL != "" || session.LoginStates[0].ProxyURL != "" {
		t.Fatal("empty account proxy must clear stale session proxy")
	}
}
