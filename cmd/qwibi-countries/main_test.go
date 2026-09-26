package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	countries "github.com/qwibi/qwibi-countries"
)

func TestCommandLine(t *testing.T) {
	for _, tc := range []struct {
		args     []string
		env      map[string]string
		code     int
		contains string
	}{
		{args: []string{"check"}, code: 0, contains: "each compatible with the one before"},
		{args: []string{"check", "--app", "0190f5a2-7c1e-7d4b-9a3f-5e2d8c1b4a60"}, code: 0, contains: "release "},
		{args: []string{"check", "--app", "not-a-uuid"}, code: 1, contains: "not a lower-case UUID"},
		{args: []string{"publish", "--version", "3.0.0", "--app", "x"}, code: 1, contains: "not declared"},
		{args: []string{"publish"}, code: 1, contains: "--app is required"},
		{args: []string{"publish", "--app", "my-countries"}, env: map[string]string{"QWIBI_ORGANIZATION_KEY": ""}, code: 1, contains: "QWIBI_ORGANIZATION_KEY"},
		{args: []string{"status", "--app", "my-countries"}, env: map[string]string{"QWIBI_ORGANIZATION_KEY": "fake"}, code: 1, contains: "the organization key was not accepted — check it was copied whole"},
		{args: []string{"frobnicate"}, code: 2, contains: "usage"},
		{args: nil, code: 2, contains: "usage"},
	} {
		for k, v := range tc.env {
			t.Setenv(k, v)
		}
		var out, errOut bytes.Buffer
		code := run(context.Background(), tc.args, &out, &errOut)
		if code != tc.code || !strings.Contains(out.String()+errOut.String(), tc.contains) {
			t.Errorf("%v: exit %d, output %q %q; want exit %d containing %q", tc.args, code, out.String(), errOut.String(), tc.code, tc.contains)
		}
	}
}

func TestCheckNotesAVersionWithUnchangedContent(t *testing.T) {
	saved := countries.Versions
	t.Cleanup(func() { countries.Versions = saved })
	next := countries.Latest()
	next.Semantic = "1.3.0"
	next.PublishedAt = next.PublishedAt.Add(time.Hour)
	countries.Versions = append(append([]countries.Version(nil), saved...), next)

	var out, errOut bytes.Buffer
	if code := run(context.Background(), []string{"check"}, &out, &errOut); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut.String())
	}
	if want := "note: 1.3.0 has the same content as 1.2.0"; !strings.Contains(out.String(), want) {
		t.Errorf("check output %q lacks %q", out.String(), want)
	}
}
