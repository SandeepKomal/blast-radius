package redact

import (
	"strings"
	"testing"
)

// Fake credentials are built at runtime so secret scanners don't flag
// this file.
var (
	fakeGitHubToken = "ghp" + "_" + strings.Repeat("a1", 18)
	fakeBearer      = "Bear" + "er " + strings.Repeat("x", 12)
)

func TestText(t *testing.T) {
	for in, want := range map[string]string{
		"email ravi.kumar@example.co.in about it":         "email [email] about it",
		"call +91 98765 43210 or 9876543210":              "call [phone] or [phone]",
		"PAN ABCPE1234F on file":                          "PAN [pan] on file",
		"Aadhaar 2345 6789 0124 for KYC":                  "Aadhaar [aadhaar] for KYC",
		"card 4111 1111 1111 1111 declined":               "card [card] declined",
		"server 10.0.12.7 is down":                        "server [ip] is down",
		"use token " + fakeGitHubToken:                    "use token [github token]",
		"Tear down the old staging servers":               "Tear down the old staging servers",
		"delete bucket logs-2026 in account 123456789012": "delete bucket logs-2026 in account 123456789012",
	} {
		if got := Text(in); got != want {
			t.Errorf("Text(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSecretsKeepCommandsReadable(t *testing.T) {
	for in, want := range map[string]string{
		"mysql --password=hunter2 -h db":                       "mysql --password=[secret] -h db",
		"vault login -token s.abcdef123456":                    "vault login -token [secret]",
		"DB_PASSWORD='p@ss word' ./migrate":                    "DB_PASSWORD=[secret] ./migrate",
		"curl -H 'Authorization: " + fakeBearer + "' api":      "curl -H 'Authorization: Bearer [secret]' api",
		"psql postgres://admin:s3cret@db:5432/app":             "psql postgres://admin:[secret]@db:5432/app",
		"aws s3 ls --profile prod AKIAIOSFODNN7EXAMPLE":        "aws s3 ls --profile prod [aws key]",
		"terraform destroy -target aws_db_instance.main":       "terraform destroy -target aws_db_instance.main",
		"kubectl delete ns payments --context 9876543210-prod": "kubectl delete ns payments --context 9876543210-prod",
	} {
		if got := Secrets(in); got != want {
			t.Errorf("Secrets(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestChecksums(t *testing.T) {
	if !verhoeff("234567890124") || verhoeff("234567890123") {
		t.Error("verhoeff")
	}
	if !luhn("4111111111111111") || luhn("4111111111111112") {
		t.Error("luhn")
	}
}
