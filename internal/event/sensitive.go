package event

import (
	"path"
	"strings"
)

// sensitiveNames are file names (compared case-insensitively) that commonly
// hold credentials.
var sensitiveNames = map[string]bool{
	".env":             true,
	".netrc":           true,
	"_netrc":           true,
	".npmrc":           true,
	".pypirc":          true,
	".git-credentials": true,
	".htpasswd":        true,
	"credentials":      true, // e.g. .aws/credentials
	"credentials.json": true, // e.g. Google service account keys
}

// sensitiveExtensions are extensions of private keys and key stores.
var sensitiveExtensions = []string{".pem", ".key", ".p12", ".pfx", ".jks", ".keystore", ".ppk"}

// sshKeyPrefixes are the default names of SSH private keys; the matching
// ".pub" files are public and not treated as sensitive.
var sshKeyPrefixes = []string{"id_rsa", "id_dsa", "id_ecdsa", "id_ed25519"}

// IsSensitivePath reports whether a repository-relative path looks like a
// file that holds secrets, so that its patch text is not recorded. This is a
// best-effort name check: it cannot find secrets in other files, and it does
// not look at file contents.
func IsSensitivePath(p string) bool {
	if p == "" {
		return false
	}
	name := strings.ToLower(path.Base(p)) // git paths always use '/'
	if sensitiveNames[name] || strings.HasPrefix(name, ".env.") {
		return true
	}
	for _, ext := range sensitiveExtensions {
		if strings.HasSuffix(name, ext) {
			return true
		}
	}
	for _, prefix := range sshKeyPrefixes {
		if strings.HasPrefix(name, prefix) && !strings.HasSuffix(name, ".pub") {
			return true
		}
	}
	return false
}
