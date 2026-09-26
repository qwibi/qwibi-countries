package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
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
