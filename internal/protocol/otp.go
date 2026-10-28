package protocol

import "regexp"

var (
	contextOTPRegex = regexp.MustCompile(`(?i)(?:openai|chatgpt|otp|code|verification|验证码|验证|代码)[^\d]{0,80}(\d{6})`)
	reverseOTPRegex = regexp.MustCompile(`(?i)(\d{6})[^\d]{0,80}(?:openai|chatgpt|otp|code|verification|验证码|验证|代码)`)
)

func extractOTP(text string) string {
	if matches := contextOTPRegex.FindStringSubmatch(text); len(matches) == 2 && validOTP(matches[1]) {
		return matches[1]
	}
	if matches := reverseOTPRegex.FindStringSubmatch(text); len(matches) == 2 && validOTP(matches[1]) {
		return matches[1]
	}
	return ""
}

func validOTP(code string) bool {
	return len(code) == 6 && code != "000000"
}

// ExtractOTP returns a six-digit code near a verification word.
// A bare number is not a code, and 000000 is never a code.
func ExtractOTP(text string) string {
	return extractOTP(text)
}
