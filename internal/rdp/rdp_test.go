package rdp

import (
	"strings"
	"testing"
)

func TestBuildRejectsInjection(t *testing.T) {
	cases := []Config{
		{Host: "10.0.0.1\r\nalternate shell:s:calc.exe"},
		{Host: "10.0.0.1", Username: "a\r\nb"},
		{Host: "10.0.0.1", Domain: "d\nx"},
		{Host: "bad:host:port"},
		{Host: "10.0.0.1", Port: 70000},
		{Host: "  "},
	}
	for _, cfg := range cases {
		if _, err := Build(cfg); err == nil {
			t.Errorf("expected error for %+v", cfg)
		}
	}
}

func TestBuildAddress(t *testing.T) {
	out, err := Build(Config{Host: "2001:db8::1"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "full address:s:[2001:db8::1]\r\n") {
		t.Errorf("ipv6 address not bracketed:\n%s", out)
	}

	out, err = Build(Config{Host: "10.0.0.1", Port: 3390})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "full address:s:10.0.0.1:3390\r\n") {
		t.Errorf("custom port missing:\n%s", out)
	}

	out, err = Build(Config{Host: "10.0.0.1"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "full address:s:10.0.0.1\r\n") {
		t.Errorf("default port should be omitted:\n%s", out)
	}
}

func TestSessionFileName(t *testing.T) {
	if got := sessionFileName(`..\..\evil`); !sessionIDPattern.MatchString(got) {
		t.Errorf("id was not replaced: %q", got)
	}
	if got := sessionFileName("0123456789abcdef"); got != "0123456789abcdef" {
		t.Errorf("valid id changed: %q", got)
	}
}
