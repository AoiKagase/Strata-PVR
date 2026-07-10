package auth

import "testing"

func TestHashAndVerifyPassword(t *testing.T) {
	hash, err := HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	if !VerifyPassword(hash, "correct horse battery staple") {
		t.Fatal("generated hash did not verify")
	}
	if VerifyPassword(hash, "wrong") {
		t.Fatal("wrong password verified")
	}
}

func TestVerifyPasswordRejectsMalformedPHC(t *testing.T) {
	for _, hash := range []string{"", "$argon2i$v=19$m=65536,t=3,p=2$bad$bad", "$argon2id$v=19$m=1,t=3,p=2$YWJjZGVmZ2g$YWJjZGVmZ2hpamtsbW5vcA"} {
		if VerifyPassword(hash, "password") {
			t.Fatalf("malformed hash verified: %q", hash)
		}
	}
}
