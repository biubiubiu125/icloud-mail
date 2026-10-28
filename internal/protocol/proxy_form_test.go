package protocol

import "testing"

func TestSplitProxyURLRoundTrip(t *testing.T) {
	parts, err := SplitProxyURL("socks5://user:p%40ss%3Aword@1.2.3.4:1080")
	if err != nil {
		t.Fatal(err)
	}
	if parts.Scheme != "socks5" || parts.Host != "1.2.3.4" || parts.Port != "1080" || parts.Username != "user" || parts.Password != "p@ss:word" {
		t.Fatalf("parts = %#v", parts)
	}
	joined, err := JoinProxyURL(parts)
	if err != nil {
		t.Fatal(err)
	}
	again, err := SplitProxyURL(joined)
	if err != nil {
		t.Fatal(err)
	}
	if again != parts {
		t.Fatalf("round trip = %#v", again)
	}
}

func TestSplitProxyURLEmptyAndPlain(t *testing.T) {
	parts, err := SplitProxyURL("  ")
	if err != nil || parts != (ProxyForm{}) {
		t.Fatalf("empty = %#v err=%v", parts, err)
	}
	parts, err = SplitProxyURL("http://1.2.3.4:8080")
	if err != nil {
		t.Fatal(err)
	}
	if parts.Scheme != "http" || parts.Host != "1.2.3.4" || parts.Port != "8080" || parts.Username != "" || parts.Password != "" {
		t.Fatalf("plain = %#v", parts)
	}
	parts, err = SplitProxyURL("socks5h://user:pass@[2001:db8::1]:1080")
	if err != nil {
		t.Fatal(err)
	}
	if parts.Scheme != "socks5h" || parts.Host != "2001:db8::1" || parts.Port != "1080" || parts.Username != "user" || parts.Password != "pass" {
		t.Fatalf("ipv6 = %#v", parts)
	}
}

func TestJoinProxyURLRejectsPartialAndClearsEmpty(t *testing.T) {
	joined, err := JoinProxyURL(ProxyForm{})
	if err != nil || joined != "" {
		t.Fatalf("empty join = %q err=%v", joined, err)
	}
	if _, err := JoinProxyURL(ProxyForm{Host: "1.2.3.4", Port: "1080"}); err == nil {
		t.Fatal("missing scheme was accepted")
	}
	if _, err := JoinProxyURL(ProxyForm{Scheme: "socks5", Username: "user"}); err == nil {
		t.Fatal("missing host was accepted")
	}
	if _, err := JoinProxyURL(ProxyForm{Scheme: "ftp", Host: "1.2.3.4", Port: "1080"}); err == nil {
		t.Fatal("bad scheme was accepted")
	}
}
