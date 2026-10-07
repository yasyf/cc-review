package github

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestCheckOf(t *testing.T) {
	cases := []struct {
		name string
		in   gqlCheckContext
		want string
	}{
		{"run in progress", gqlCheckContext{Typename: "CheckRun", Status: "IN_PROGRESS"}, "PENDING"},
		{"run queued", gqlCheckContext{Typename: "CheckRun", Status: "QUEUED"}, "PENDING"},
		{"run success", gqlCheckContext{Typename: "CheckRun", Status: "COMPLETED", Conclusion: "SUCCESS"}, "SUCCESS"},
		{"run skipped", gqlCheckContext{Typename: "CheckRun", Status: "COMPLETED", Conclusion: "SKIPPED"}, "SKIPPED"},
		{"run neutral", gqlCheckContext{Typename: "CheckRun", Status: "COMPLETED", Conclusion: "NEUTRAL"}, "NEUTRAL"},
		{"run stale", gqlCheckContext{Typename: "CheckRun", Status: "COMPLETED", Conclusion: "STALE"}, "NEUTRAL"},
		{"run cancelled", gqlCheckContext{Typename: "CheckRun", Status: "COMPLETED", Conclusion: "CANCELLED"}, "FAILURE"},
		{"run timed out", gqlCheckContext{Typename: "CheckRun", Status: "COMPLETED", Conclusion: "TIMED_OUT"}, "FAILURE"},
		{"status success", gqlCheckContext{Typename: "StatusContext", State: "SUCCESS"}, "SUCCESS"},
		{"status expected", gqlCheckContext{Typename: "StatusContext", State: "EXPECTED"}, "PENDING"},
		{"status error", gqlCheckContext{Typename: "StatusContext", State: "ERROR"}, "FAILURE"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := checkOf(tc.in).State; got != tc.want {
				t.Fatalf("checkOf(%+v).State = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestReviewersOfLetsARequestSupersedeAnOldReview(t *testing.T) {
	var g gqlPR
	err := json.Unmarshal([]byte(`{
	  "latestReviews": {"nodes": [
	    {"state": "APPROVED", "author": {"__typename": "User", "login": "carol"}},
	    {"state": "DISMISSED", "author": {"__typename": "User", "login": "erin"}}
	  ]},
	  "reviewRequests": {"nodes": [
	    {"requestedReviewer": {"__typename": "User", "login": "carol"}},
	    {"requestedReviewer": {"__typename": "Team", "combinedSlug": "acme/core"}}
	  ]}
	}`), &g)
	if err != nil {
		t.Fatal(err)
	}
	got := reviewersOf(g)
	want := []Reviewer{{Login: "carol", State: "PENDING"}, {Login: "acme/core", State: "PENDING"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("reviewersOf = %+v, want %+v", got, want)
	}
}
