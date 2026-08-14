package api

import "testing"

// What a suggestion is allowed to be about.
//
// "Already in the files: Sites" turned up on a project that had pulled three
// hundred files from Moodle, one of which happened to be called index.html.
// A named file counts where it was meant — at the project's top level.
func TestASuggestionIsAboutTheProjectNotItsCorners(t *testing.T) {
	site := []string{"index.html"}
	if !ownsFile(site, "index.html") {
		t.Error("a site at the top is not recognised")
	}
	if ownsFile(site, "Semester1/Grundlagen/index.html") {
		t.Error("a pulled page three folders down still counts as a website")
	}

	// A kind of file is different: an .eml is an .eml wherever it lies.
	mail := []string{"*.eml", "labels.json"}
	if !ownsFile(mail, "inbox/2025/a-message.eml") {
		t.Error("mail in a folder is not recognised")
	}
	if ownsFile(mail, "inbox/labels.json") {
		t.Error("a named file in a folder counts, and should not")
	}

	// A pattern that names its folder stays exact.
	cal := []string{"calendar.ics", "split/*.ics"}
	if !ownsFile(cal, "split/term.ics") {
		t.Error("the folder pattern is not recognised")
	}
	if ownsFile(cal, "other/term.ics") {
		t.Error("another folder should not match")
	}
}
