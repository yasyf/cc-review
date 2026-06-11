package attrib

import (
	"context"
	"fmt"
	"reflect"
	"slices"
	"testing"

	"github.com/yasyf/cc-review/internal/store"
)

type fakeDiffer struct {
	patches map[string]string
	calls   []string
}

func (d *fakeDiffer) Diff(_ context.Context, from, to string) (string, error) {
	key := from + "->" + to
	d.calls = append(d.calls, key)
	patch, ok := d.patches[key]
	if !ok {
		return "", fmt.Errorf("no patch for %s", key)
	}
	return patch, nil
}

const singleTurnPatch = `diff --git a/a.txt b/a.txt
--- a/a.txt
+++ b/a.txt
@@ -1,2 +1,4 @@
 one
+new1
+new2
 two
`

const twoRegionsTurn1 = `diff --git a/a.txt b/a.txt
--- a/a.txt
+++ b/a.txt
@@ -1,2 +1,3 @@
 l1
+x
 l2
`

const twoRegionsTurn2 = `diff --git a/a.txt b/a.txt
--- a/a.txt
+++ b/a.txt
@@ -5,3 +5,4 @@
 l4
 l5
+y
 l6
`

const twoRegionsVersion = `diff --git a/a.txt b/a.txt
--- a/a.txt
+++ b/a.txt
@@ -1,6 +1,8 @@
 l1
+x
 l2
 l3
 l4
 l5
+y
 l6
`

const gapTurn1 = `diff --git a/a.txt b/a.txt
--- a/a.txt
+++ b/a.txt
@@ -1,2 +1,3 @@
 l1
+x
 l2
`

const gapManual = `diff --git a/a.txt b/a.txt
--- a/a.txt
+++ b/a.txt
@@ -2,2 +2,3 @@
 x
 l2
+m
`

const gapTurn2 = `diff --git a/a.txt b/a.txt
--- a/a.txt
+++ b/a.txt
@@ -3,2 +3,3 @@
 l2
 m
+y
`

const gapVersion = `diff --git a/a.txt b/a.txt
--- a/a.txt
+++ b/a.txt
@@ -1,2 +1,5 @@
 l1
+x
 l2
+m
+y
`

const deleteShiftTurn1 = `diff --git a/a.txt b/a.txt
--- a/a.txt
+++ b/a.txt
@@ -1,2 +1,5 @@
 l1
+a
+b
+c
 l2
`

const deleteShiftTurn2 = `diff --git a/a.txt b/a.txt
--- a/a.txt
+++ b/a.txt
@@ -2,4 +2,4 @@
 a
-b
 c
 l2
+z
`

const deleteShiftVersion = `diff --git a/a.txt b/a.txt
--- a/a.txt
+++ b/a.txt
@@ -1,2 +1,5 @@
 l1
+a
+c
 l2
+z
`

const renameTurn1 = `diff --git a/old.txt b/old.txt
--- a/old.txt
+++ b/old.txt
@@ -1,2 +1,3 @@
 l1
 l2
+x
`

const renameTurn2 = `diff --git a/old.txt b/new.txt
rename from old.txt
rename to new.txt
--- a/old.txt
+++ b/new.txt
@@ -1,3 +1,4 @@
+y
 l1
 l2
 x
`

const renameVersion = `diff --git a/old.txt b/new.txt
rename from old.txt
rename to new.txt
--- a/old.txt
+++ b/new.txt
@@ -1,2 +1,4 @@
+y
 l1
 l2
+x
`

const newFilePatch = `diff --git a/b.txt b/b.txt
new file mode 100644
--- /dev/null
+++ b/b.txt
@@ -0,0 +1,3 @@
+n1
+n2
+n3
`

const recreateDelete = `diff --git a/a.txt b/a.txt
deleted file mode 100644
--- a/a.txt
+++ /dev/null
@@ -1,2 +0,0 @@
-l1
-l2
`

const recreateCreate = `diff --git a/a.txt b/a.txt
new file mode 100644
--- /dev/null
+++ b/a.txt
@@ -0,0 +1,2 @@
+r1
+r2
`

const recreateVersion = `diff --git a/a.txt b/a.txt
--- a/a.txt
+++ b/a.txt
@@ -1,2 +1,2 @@
-l1
-l2
+r1
+r2
`

const sameTreeVersion = `diff --git a/a.txt b/a.txt
--- a/a.txt
+++ b/a.txt
@@ -1,1 +1,2 @@
 l1
+m
`

const binaryAndTextPatch = `diff --git a/img.bin b/img.bin
index 1111111..2222222 100644
Binary files a/img.bin and b/img.bin differ
diff --git a/a.txt b/a.txt
--- a/a.txt
+++ b/a.txt
@@ -1,1 +1,2 @@
 l1
+x
`

const unseenFileVersion = `diff --git a/c.txt b/c.txt
--- a/c.txt
+++ b/c.txt
@@ -1,2 +1,3 @@
 c1
+added
 c2
`

func TestCompute(t *testing.T) {
	for _, tc := range []struct {
		name         string
		patches      map[string]string
		chain        []Link
		versionPatch string
		want         map[string][]store.AttributionRange
		wantCalls    []string
	}{
		{
			name:         "single turn adding lines",
			patches:      map[string]string{"base->a": singleTurnPatch},
			chain:        []Link{{From: "base", To: "a", TurnID: 10}},
			versionPatch: singleTurnPatch,
			want: map[string][]store.AttributionRange{
				"a.txt": {{Start: 2, End: 3, TurnID: 10}},
			},
			wantCalls: []string{"base->a"},
		},
		{
			name: "two turns editing different regions",
			patches: map[string]string{
				"base->a": twoRegionsTurn1,
				"a->b":    twoRegionsTurn2,
			},
			chain: []Link{
				{From: "base", To: "a", TurnID: 10},
				{From: "a", To: "b", TurnID: 20},
			},
			versionPatch: twoRegionsVersion,
			want: map[string][]store.AttributionRange{
				"a.txt": {
					{Start: 2, End: 2, TurnID: 10},
					{Start: 7, End: 7, TurnID: 20},
				},
			},
			wantCalls: []string{"base->a", "a->b"},
		},
		{
			name: "untagged gap between turns",
			patches: map[string]string{
				"base->a": gapTurn1,
				"a->m":    gapManual,
				"m->b":    gapTurn2,
			},
			chain: []Link{
				{From: "base", To: "a", TurnID: 10},
				{From: "a", To: "m", TurnID: 0},
				{From: "m", To: "b", TurnID: 20},
			},
			versionPatch: gapVersion,
			want: map[string][]store.AttributionRange{
				"a.txt": {
					{Start: 2, End: 2, TurnID: 10},
					{Start: 4, End: 4, TurnID: 0},
					{Start: 5, End: 5, TurnID: 20},
				},
			},
			wantCalls: []string{"base->a", "a->m", "m->b"},
		},
		{
			name: "later turn deletes earlier turn's lines",
			patches: map[string]string{
				"base->a": deleteShiftTurn1,
				"a->b":    deleteShiftTurn2,
			},
			chain: []Link{
				{From: "base", To: "a", TurnID: 10},
				{From: "a", To: "b", TurnID: 20},
			},
			versionPatch: deleteShiftVersion,
			want: map[string][]store.AttributionRange{
				"a.txt": {
					{Start: 2, End: 3, TurnID: 10},
					{Start: 5, End: 5, TurnID: 20},
				},
			},
			wantCalls: []string{"base->a", "a->b"},
		},
		{
			name: "rename with edits",
			patches: map[string]string{
				"base->a": renameTurn1,
				"a->b":    renameTurn2,
			},
			chain: []Link{
				{From: "base", To: "a", TurnID: 10},
				{From: "a", To: "b", TurnID: 20},
			},
			versionPatch: renameVersion,
			want: map[string][]store.AttributionRange{
				"new.txt": {
					{Start: 1, End: 1, TurnID: 20},
					{Start: 4, End: 4, TurnID: 10},
				},
			},
			wantCalls: []string{"base->a", "a->b"},
		},
		{
			name:         "file created by a turn is fully tagged",
			patches:      map[string]string{"base->a": newFilePatch},
			chain:        []Link{{From: "base", To: "a", TurnID: 10}},
			versionPatch: newFilePatch,
			want: map[string][]store.AttributionRange{
				"b.txt": {{Start: 1, End: 3, TurnID: 10}},
			},
			wantCalls: []string{"base->a"},
		},
		{
			name: "delete then recreate owns every line",
			patches: map[string]string{
				"base->a": recreateDelete,
				"a->b":    recreateCreate,
			},
			chain: []Link{
				{From: "base", To: "a", TurnID: 10},
				{From: "a", To: "b", TurnID: 20},
			},
			versionPatch: recreateVersion,
			want: map[string][]store.AttributionRange{
				"a.txt": {{Start: 1, End: 2, TurnID: 20}},
			},
			wantCalls: []string{"base->a", "a->b"},
		},
		{
			name:         "from equals to skips diff",
			patches:      map[string]string{},
			chain:        []Link{{From: "same", To: "same", TurnID: 10}},
			versionPatch: sameTreeVersion,
			want: map[string][]store.AttributionRange{
				"a.txt": {{Start: 2, End: 2, TurnID: 0}},
			},
			wantCalls: nil,
		},
		{
			name:         "binary file skipped",
			patches:      map[string]string{"base->a": binaryAndTextPatch},
			chain:        []Link{{From: "base", To: "a", TurnID: 10}},
			versionPatch: binaryAndTextPatch,
			want: map[string][]store.AttributionRange{
				"a.txt": {{Start: 2, End: 2, TurnID: 10}},
			},
			wantCalls: []string{"base->a"},
		},
		{
			name:         "version file unseen by chain is untagged",
			patches:      map[string]string{"base->a": singleTurnPatch},
			chain:        []Link{{From: "base", To: "a", TurnID: 10}},
			versionPatch: unseenFileVersion,
			want: map[string][]store.AttributionRange{
				"c.txt": {{Start: 2, End: 2, TurnID: 0}},
			},
			wantCalls: []string{"base->a"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := &fakeDiffer{patches: tc.patches}
			got, err := Compute(context.Background(), d, tc.chain, tc.versionPatch)
			if err != nil {
				t.Fatalf("Compute: %v", err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("ranges:\n got %#v\nwant %#v", got, tc.want)
			}
			if !slices.Equal(d.calls, tc.wantCalls) {
				t.Errorf("diff calls: got %v want %v", d.calls, tc.wantCalls)
			}
		})
	}
}
