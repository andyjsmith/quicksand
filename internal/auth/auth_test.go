package auth

import "testing"

func TestPasswordHash(t *testing.T) {
	hash, err := HashPassword("correct horse")
	if err != nil {
		t.Fatal(err)
	}
	if !CheckPassword(hash, "correct horse") {
		t.Error("correct password rejected")
	}
	if CheckPassword(hash, "wrong horse") {
		t.Error("wrong password accepted")
	}
	if other, _ := HashPassword("correct horse"); other == hash {
		t.Error("hashes are not salted")
	}
	for _, bad := range []string{"", "plain", "pbkdf2-sha256$x$y$z", "md5$1$a$b"} {
		if CheckPassword(bad, "correct horse") {
			t.Errorf("malformed hash %q accepted", bad)
		}
	}
}
