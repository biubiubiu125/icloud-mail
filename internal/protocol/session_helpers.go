package protocol

import "strings"

func iCloudWebLoginSaved(session ICloudSession) bool {
	hasWebState := false
	for _, state := range session.LoginStates {
		if state.Kind != LoginStateICloudWeb {
			continue
		}
		hasWebState = true
		if len(state.Cookies) > 0 || len(session.Cookies) > 0 {
			return true
		}
	}
	if hasWebState && len(session.Cookies) > 0 {
		return true
	}
	if len(session.LoginStates) == 0 {
		return len(session.Cookies) > 0
	}
	if !hasAnyNonAppleAccountLoginState(session.LoginStates) {
		return false
	}
	return len(session.Cookies) > 0
}

func normalizeICloudIMAPEmail(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func publicErrorMessage(err error) string {
	return PublicMessage(err)
}

const (
	DefaultIMAPHost = "imap.mail.me.com"
	DefaultIMAPPort = 993
)

// IMAPLoginState returns the saved IMAP login for a session, if it is usable.
func IMAPLoginState(session ICloudSession) (LoginState, bool) {
	for _, state := range session.LoginStates {
		if state.Kind != LoginStateICloudIMAP {
			continue
		}
		email := normalizeICloudIMAPEmail(state.IMAPEmail)
		if email == "" {
			email = normalizeICloudIMAPEmail(session.AppleID)
		}
		if email == "" || strings.TrimSpace(state.IMAPAppPassword) == "" {
			continue
		}
		state.IMAPEmail = email
		state.IMAPUsername = firstNonEmpty(strings.TrimSpace(state.IMAPUsername), email)
		state.IMAPHost = firstNonEmpty(strings.TrimSpace(state.IMAPHost), DefaultIMAPHost)
		state.ProxyURL = session.ProxyURL
		if state.IMAPPort == 0 {
			state.IMAPPort = DefaultIMAPPort
		}
		return state, true
	}
	return LoginState{}, false
}

// WithIMAPLoginState replaces or appends the IMAP login without touching other kinds.
func WithIMAPLoginState(session ICloudSession, next LoginState) ICloudSession {
	next.Kind = LoginStateICloudIMAP
	next.IMAPEmail = normalizeICloudIMAPEmail(firstNonEmpty(next.IMAPEmail, session.AppleID))
	next.IMAPUsername = firstNonEmpty(strings.TrimSpace(next.IMAPUsername), next.IMAPEmail)
	next.IMAPHost = firstNonEmpty(strings.TrimSpace(next.IMAPHost), DefaultIMAPHost)
	next.Host = firstNonEmpty(strings.TrimSpace(next.Host), next.IMAPHost)
	next.Origin = firstNonEmpty(strings.TrimSpace(next.Origin), "imaps://"+next.IMAPHost)
	if next.IMAPPort == 0 {
		next.IMAPPort = DefaultIMAPPort
	}
	next.ProxyURL = session.ProxyURL
	for i, state := range session.LoginStates {
		if state.Kind == LoginStateICloudIMAP {
			session.LoginStates[i] = next
			return session
		}
	}
	session.LoginStates = append(session.LoginStates, next)
	return session
}

// ApplyAccountProxy copies the account proxy onto the session used for a provider call.
// An empty account proxy is intentional and clears any stale session proxy.
func ApplyAccountProxy(session ICloudSession, accountProxy string) ICloudSession {
	accountProxy = strings.TrimSpace(accountProxy)
	session.ProxyURL = accountProxy
	for i := range session.LoginStates {
		session.LoginStates[i].ProxyURL = accountProxy
	}
	return session
}

// SessionAccountMismatch is true when both sides name an account and they differ.
// An empty incoming account id is a historical session and may be bound to the row.
func SessionAccountMismatch(existingID, incomingID string) bool {
	existingID = strings.TrimSpace(existingID)
	incomingID = strings.TrimSpace(incomingID)
	return existingID != "" && incomingID != "" && existingID != incomingID
}

// MergeSavedSession keeps login kinds that the incoming save did not replace.
// accountProxy is always the account row, never a value discovered inside incoming.
func MergeSavedSession(existing, incoming ICloudSession, accountProxy string) ICloudSession {
	out := existing
	if strings.TrimSpace(incoming.AppleID) != "" {
		out.AppleID = strings.TrimSpace(incoming.AppleID)
	}
	if strings.TrimSpace(incoming.DSID) != "" {
		out.DSID = incoming.DSID
	}
	if strings.TrimSpace(incoming.ClientID) != "" {
		out.ClientID = incoming.ClientID
	}
	if strings.TrimSpace(incoming.Host) != "" {
		out.Host = incoming.Host
	}
	if strings.TrimSpace(incoming.ClientBuildNumber) != "" {
		out.ClientBuildNumber = incoming.ClientBuildNumber
	}
	if strings.TrimSpace(incoming.MasteringNumber) != "" {
		out.MasteringNumber = incoming.MasteringNumber
	}
	if strings.TrimSpace(incoming.PremiumMailBaseURL) != "" {
		out.PremiumMailBaseURL = incoming.PremiumMailBaseURL
	}
	if strings.TrimSpace(incoming.MailGatewayBaseURL) != "" {
		out.MailGatewayBaseURL = incoming.MailGatewayBaseURL
	}
	if strings.TrimSpace(incoming.MailBaseURL) != "" {
		out.MailBaseURL = incoming.MailBaseURL
	}
	if incoming.CapabilitiesAuthoritative || incoming.CanCreateHME || incoming.IsICloudPlus {
		out.IsICloudPlus = incoming.IsICloudPlus
		out.CanCreateHME = incoming.CanCreateHME
		out.CapabilitiesAuthoritative = incoming.CapabilitiesAuthoritative
	}
	if !incoming.SavedAt.IsZero() {
		out.SavedAt = incoming.SavedAt
	}
	if strings.TrimSpace(incoming.Note) != "" {
		out.Note = incoming.Note
	}
	replaced := map[string]struct{}{}
	for _, state := range incoming.LoginStates {
		if strings.TrimSpace(state.Kind) == "" {
			continue
		}
		replaced[state.Kind] = struct{}{}
		found := false
		for i := range out.LoginStates {
			if out.LoginStates[i].Kind == state.Kind {
				out.LoginStates[i] = state
				found = true
				break
			}
		}
		if !found {
			out.LoginStates = append(out.LoginStates, state)
		}
	}
	if len(incoming.Cookies) > 0 && len(replaced) == 0 {
		out.Cookies = incoming.Cookies
	}
	if _, ok := replaced[LoginStateICloudWeb]; ok && len(incoming.Cookies) > 0 {
		out.Cookies = incoming.Cookies
	}
	return ApplyAccountProxy(out, accountProxy)
}
