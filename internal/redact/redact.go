// Package redact masks personal data and secrets in text before
// blast-radius writes it to the audit log, so the log keeps what happened
// without keeping who it was about or the keys that were used.
package redact

import (
	"regexp"
	"strings"
)

// Secrets are masked everywhere, including in commands. The key is kept so
// the command still reads correctly: --password=[secret].
var secrets = []struct {
	re   *regexp.Regexp
	repl string
}{
	{regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----[\s\S]*?-----END [A-Z ]*PRIVATE KEY-----`), "[private key]"},
	{regexp.MustCompile(`\b(?:AKIA|ASIA)[0-9A-Z]{16}\b`), "[aws key]"},
	{regexp.MustCompile(`\bgh[pousr]_[A-Za-z0-9]{30,}\b|\bgithub_pat_[A-Za-z0-9_]{30,}\b`), "[github token]"},
	{regexp.MustCompile(`\bxox[abprs]-[A-Za-z0-9-]{10,}\b`), "[slack token]"},
	{regexp.MustCompile(`\bsk-[A-Za-z0-9_-]{20,}\b`), "[api key]"},
	{regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\b`), "[jwt]"},
	{regexp.MustCompile(`(?i)\b(bearer|basic)\s+[A-Za-z0-9._~+/=-]{8,}`), "$1 [secret]"},
	{regexp.MustCompile(`([a-z][a-z0-9+.-]*://[^/\s:@]+:)[^@\s/]+@`), "${1}[secret]@"},
	{regexp.MustCompile(`(?i)(--?(?:password|passwd|pass|token|secret|api-key|apikey|access-key|secret-key|client-secret)(?:=|\s+))("[^"]*"|'[^']*'|\S+)`), "${1}[secret]"},
	{regexp.MustCompile(`(?i)\b([A-Z0-9_]*(?:PASSWORD|PASSWD|SECRET|TOKEN|API_KEY|APIKEY|ACCESS_KEY|PRIVATE_KEY)[A-Z0-9_]*=)("[^"]*"|'[^']*'|\S+)`), "${1}[secret]"},
}

// Personal data is masked in free text (requests and error messages), not
// in commands, where long numbers are usually account or resource IDs.
var (
	email = regexp.MustCompile(`[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}`)
	pan   = regexp.MustCompile(`\b[A-Z]{3}[ABCFGHLJPT][A-Z][0-9]{4}[A-Z]\b`)
	// Indian mobile numbers: optional +91 / 91 / 0, then 10 digits from 6-9.
	phone = regexp.MustCompile(`(?:\+91[\s-]?|\b91[\s-]?|\b0)?\b[6-9]\d{4}[\s-]?\d{5}\b`)
	// Runs of 12 to 19 digits, with optional single spaces or dashes, are
	// Aadhaar numbers (Verhoeff check) or card numbers (Luhn check).
	digits = regexp.MustCompile(`\b\d(?:[ -]?\d){11,18}\b`)
	ipv4   = regexp.MustCompile(`\b(?:\d{1,3}\.){3}\d{1,3}\b`)
)

// Secrets masks keys, tokens and passwords.
func Secrets(s string) string {
	for _, r := range secrets {
		s = r.re.ReplaceAllString(s, r.repl)
	}
	return s
}

// Text masks secrets and personal data: emails, Indian phone numbers, PAN,
// Aadhaar and payment card numbers, and IP addresses.
func Text(s string) string {
	s = Secrets(s)
	s = email.ReplaceAllString(s, "[email]")
	s = digits.ReplaceAllStringFunc(s, func(m string) string {
		d := strings.NewReplacer(" ", "", "-", "").Replace(m)
		switch {
		case len(d) == 12 && d[0] >= '2' && verhoeff(d):
			return "[aadhaar]"
		case len(d) >= 13 && luhn(d):
			return "[card]"
		}
		return m
	})
	s = pan.ReplaceAllString(s, "[pan]")
	s = phone.ReplaceAllString(s, "[phone]")
	s = ipv4.ReplaceAllString(s, "[ip]")
	return s
}

func luhn(d string) bool {
	sum, double := 0, false
	for i := len(d) - 1; i >= 0; i-- {
		n := int(d[i] - '0')
		if double {
			if n *= 2; n > 9 {
				n -= 9
			}
		}
		sum += n
		double = !double
	}
	return sum%10 == 0
}

// verhoeff validates the check digit Aadhaar numbers use.
func verhoeff(d string) bool {
	mul := [10][10]int{
		{0, 1, 2, 3, 4, 5, 6, 7, 8, 9}, {1, 2, 3, 4, 0, 6, 7, 8, 9, 5},
		{2, 3, 4, 0, 1, 7, 8, 9, 5, 6}, {3, 4, 0, 1, 2, 8, 9, 5, 6, 7},
		{4, 0, 1, 2, 3, 9, 5, 6, 7, 8}, {5, 9, 8, 7, 6, 0, 4, 3, 2, 1},
		{6, 5, 9, 8, 7, 1, 0, 4, 3, 2}, {7, 6, 5, 9, 8, 2, 1, 0, 4, 3},
		{8, 7, 6, 5, 9, 3, 2, 1, 0, 4}, {9, 8, 7, 6, 5, 4, 3, 2, 1, 0},
	}
	perm := [8][10]int{
		{0, 1, 2, 3, 4, 5, 6, 7, 8, 9}, {1, 5, 7, 6, 2, 8, 3, 0, 9, 4},
		{5, 8, 0, 3, 7, 9, 6, 1, 4, 2}, {8, 9, 1, 6, 0, 4, 3, 5, 2, 7},
		{9, 4, 5, 3, 1, 2, 6, 8, 7, 0}, {4, 2, 8, 6, 5, 7, 3, 9, 0, 1},
		{2, 7, 9, 3, 8, 0, 6, 4, 1, 5}, {7, 0, 4, 6, 9, 1, 3, 2, 5, 8},
	}
	c := 0
	for i := 0; i < len(d); i++ {
		c = mul[c][perm[i%8][int(d[len(d)-1-i]-'0')]]
	}
	return c == 0
}
