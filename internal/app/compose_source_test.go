package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNoOpSaveRetainsRevisionDirectoryAndInvalidatesReview(t *testing.T) {
	l, _ := coordinationLibrary(t)
	draft, _ := l.definition("example")
	plan, err := l.review(context.Background(), "example", "")
	if err != nil {
		t.Fatal(err)
	}
	after, err := l.edit(context.Background(), "example", editInput{Compose: draft.Compose, Revision: draft.Fingerprint}, true)
	if err != nil || after.ConfigRevision != "" || after.EditVersion != draft.EditVersion+1 {
		t.Fatal("no-op created a configuration revision", err)
	}
	dir, _ := l.directory("example")
	if _, err := os.Stat(filepath.Join(dir, "revisions")); !os.IsNotExist(err) {
		t.Fatal("no-op duplicated the initial bundle")
	}
	if _, err := l.startJob(context.Background(), "example", plan.ID, "example"); err == nil {
		t.Fatal("same-content save did not invalidate the old review")
	}
}

func TestComposeSourcePreservesNoOpBytes(t *testing.T) {
	for _, raw := range []string{
		"# header\r\nservices:\r\n  web: # keep service\r\n    image: 'nginx:alpine' # pinned\r\n    environment: {TOKEN: 'fixture-secret', EMPTY: null} # keep env\r\n",
		"# header\nservices:\n  web:\n    image: nginx:alpine\n    environment:\n      MESSAGE: | # block comment\n        fixture-secret\n        second line\n",
		"services:\n  web:\n    image: nginx:alpine\n    environment: ['TOKEN=fixture-secret', INHERITED]\n",
		"# no secrets\nservices: {web: {image: 'nginx:alpine'}} # ending\n",
		"x-env: &shared {TOKEN: fixture-secret}\nservices:\n  web:\n    image: nginx:alpine\n    environment: *shared\n",
		"x-base: &base\n  image: nginx:alpine\n  environment: {TOKEN: fixture-secret}\nservices:\n  web:\n    <<: *base # keep merge\n",
	} {
		t.Run(raw[:min(20, len(raw))], func(t *testing.T) {
			_, preview, _, err := inspectCompose(raw, true)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(preview, "fixture-secret") {
				t.Fatal("secret reached preview")
			}
			restored, err := restoreHidden(preview, raw)
			if err != nil || restored != raw {
				t.Fatal("no-op changed source bytes", err)
			}
		})
	}
}

func TestComposeSourceEditsKeepSurroundingTextAndSecrets(t *testing.T) {
	raw := "# héader\r\nservices:\r\n  web: # service comment\r\n    image: 'nginx:alpine' # pin\r\n    environment: {TOKEN: 'fixture-secret', NUMBER: 12} # retained\r\n    labels: {description: 'an ordinary label'}\r\n"
	l := newLibrary(t.TempDir(), "", false, &fixtureSource{}, &fixtureRunner{})
	if _, err := l.create(context.Background(), stackInput{Name: "example", Compose: raw}); err != nil {
		t.Fatal(err)
	}
	definition, err := l.definition("example")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(definition.Compose, "# héader\r\n") || !strings.Contains(definition.Compose, "image: 'nginx:alpine' # pin\r\n") {
		t.Fatal("preview rewrote non-secret source")
	}
	edited := strings.Replace(definition.Compose, "nginx:alpine", "nginx:stable", 1)
	record, err := l.edit(context.Background(), "example", editInput{Compose: edited, Revision: definition.Fingerprint}, true)
	if err != nil {
		t.Fatal(err)
	}
	files, err := l.content(record)
	if err != nil {
		t.Fatal(err)
	}
	actual := files["compose.yaml"]
	for _, fragment := range []string{"# héader\r\n", "web: # service comment\r\n", "image: 'nginx:stable' # pin\r\n", "# retained\r\n", "labels: {description: 'an ordinary label'}\r\n", "fixture-secret"} {
		if !strings.Contains(actual, fragment) {
			t.Fatal("edited source lost formatting/value", fragment)
		}
	}
	if strings.Contains(actual, "[hidden]") {
		t.Fatal("saved a preview placeholder")
	}
}

func TestComposeSourceComplexSecretCommentsAndListReordering(t *testing.T) {
	raw := "# header\nservices:\n  web:\n    image: nginx:alpine\n    environment:\n      TOKEN: | # secret annotation\n        fixture-secret\n        multiline\n"
	_, preview, _, err := inspectCompose(raw, true)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := restoreHidden(strings.Replace(preview, "nginx:alpine", "nginx:stable", 1), raw)
	if err != nil || !strings.Contains(restored, "# header") || !strings.Contains(restored, "# secret annotation") || !strings.Contains(restored, "fixture-secret") {
		t.Fatal("complex source lost comments or secret", err)
	}
	list := "services:\n  web:\n    image: nginx:alpine\n    environment: ['FIRST=fixture-first', 'SECOND=fixture-second'] # retained\n"
	_, preview, _, err = inspectCompose(list, true)
	if err != nil {
		t.Fatal(err)
	}
	preview = strings.Replace(preview, `"FIRST=[hidden]", "SECOND=[hidden]"`, `"SECOND=[hidden]", "FIRST=[hidden]"`, 1)
	restored, err = restoreHidden(preview, list)
	if err != nil || !strings.Contains(restored, "fixture-second") || !strings.Contains(restored, "# retained") {
		t.Fatal(err)
	}
	if _, err := restoreHidden(strings.Replace(preview, "SECOND=[hidden]", "UNKNOWN=[hidden]", 1), list); err == nil {
		t.Fatal("unmatched hidden variable was accepted")
	}
}
