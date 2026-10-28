package protocol

import (
	"net"
	"net/url"
	"strings"
)

// ProxyForm is the editable view of an account proxy. Empty means direct connect.
type ProxyForm struct {
	Scheme   string
	Host     string
	Port     string
	Username string
	Password string
}

func SplitProxyURL(raw string) (ProxyForm, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ProxyForm{}, nil
	}
	normalized, err := normalizeProxyURL(raw)
	if err != nil {
		return ProxyForm{}, err
	}
	parsed, err := url.Parse(normalized)
	if err != nil {
		return ProxyForm{}, err
	}
	form := ProxyForm{Scheme: parsed.Scheme, Host: parsed.Hostname(), Port: parsed.Port()}
	if parsed.User != nil {
		form.Username = parsed.User.Username()
		form.Password, _ = parsed.User.Password()
	}
	return form, nil
}

func JoinProxyURL(form ProxyForm) (string, error) {
	form.Scheme = strings.ToLower(strings.TrimSpace(form.Scheme))
	form.Host = strings.TrimSpace(form.Host)
	form.Port = strings.TrimSpace(form.Port)
	form.Username = strings.TrimSpace(form.Username)
	if strings.HasPrefix(form.Host, "[") && strings.HasSuffix(form.Host, "]") {
		form.Host = form.Host[1 : len(form.Host)-1]
	}
	if form.Scheme == "" && form.Host == "" && form.Port == "" && form.Username == "" && form.Password == "" {
		return "", nil
	}
	if form.Scheme == "" || form.Host == "" {
		return "", NewError("invalid_proxy_url", "代理地址缺少协议或主机", false)
	}
	target := &url.URL{Scheme: form.Scheme, Host: form.Host}
	if form.Port != "" {
		target.Host = net.JoinHostPort(form.Host, form.Port)
	} else if strings.Contains(form.Host, ":") {
		target.Host = "[" + form.Host + "]"
	}
	switch {
	case form.Password != "":
		target.User = url.UserPassword(form.Username, form.Password)
	case form.Username != "":
		target.User = url.User(form.Username)
	}
	return normalizeProxyURL(target.String())
}
