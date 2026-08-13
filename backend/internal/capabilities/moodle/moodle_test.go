package moodle

import "testing"

// What people paste in is the address bar, and the address bar is the login
// page. Every one of these has to end up at the same place, because getting it
// wrong used to cost a password.
func TestBaseURL(t *testing.T) {
	const want = "https://elearning.dhbw-ravensburg.de"
	for _, in := range []string{
		"https://elearning.dhbw-ravensburg.de",
		"https://elearning.dhbw-ravensburg.de/",
		"  https://elearning.dhbw-ravensburg.de/login/index.php ",
		"https://elearning.dhbw-ravensburg.de/login/token.php",
		"https://elearning.dhbw-ravensburg.de/login/",
		"https://elearning.dhbw-ravensburg.de/my/",
		"https://elearning.dhbw-ravensburg.de/my/index.php",
		"https://elearning.dhbw-ravensburg.de/course/index.php",
	} {
		if got := baseURL(in); got != want {
			t.Errorf("baseURL(%q) = %q, want %q", in, got, want)
		}
	}
	// A Moodle that lives in a sub-folder keeps its sub-folder.
	if got := baseURL("https://example.org/moodle/login/index.php"); got != "https://example.org/moodle" {
		t.Errorf("sub-folder lost: %q", got)
	}
}
