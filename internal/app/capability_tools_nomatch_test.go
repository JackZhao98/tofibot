package app

import "testing"

func TestQueryFitsNeedsHalfTheTermsInTheBestTool(t *testing.T) {
	by := map[string]Tool{"a": {Name: "a", Description: "capture a screenshot of the screen"}}
	if !queryFits(by, []string{"a"}, "capture screenshot") {
		t.Fatal("a covered query did not fit")
	}
	if queryFits(by, []string{"a"}, "zebra giraffe lion screenshot") || queryFits(by, nil, "capture") {
		t.Fatal("an uncovered query fit")
	}
}
