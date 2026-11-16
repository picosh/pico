package db

import "testing"

func TestIsCertKey(t *testing.T) {
	for key, want := range map[string]bool{
		"ssh-ed25519-cert-v01@openssh.com AAAA comment": true,
		"ecdsa-sha2-nistp256-cert-v01@openssh.com AAAA": true,
		"  ssh-rsa-cert-v01@openssh.com AAAA":           true,
		"ssh-ed25519 AAAA comment":                      false,
		"sk-ssh-ed25519@openssh.com AAAA":               false,
		"":                                              false,
	} {
		if got := IsCertKey(key); got != want {
			t.Errorf("IsCertKey(%q) = %v, want %v", key, got, want)
		}
	}
}
