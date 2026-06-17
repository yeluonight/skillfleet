package daemon

import (
	"strings"
	"testing"
)

func TestRenderUnitSubstitutesExecStart(t *testing.T) {
	out, err := renderUnit(DefaultUnit, Spec{
		Description: "test",
		BinaryPath:  "/usr/local/bin/sf",
		Args:        []string{"-config", "/etc/sf.yaml"},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := "ExecStart=/usr/local/bin/sf -config /etc/sf.yaml"
	if !strings.Contains(out, want) {
		t.Fatalf("rendered unit missing %q\n%s", want, out)
	}
	if !strings.Contains(out, "Description=test") {
		t.Fatalf("rendered unit missing description\n%s", out)
	}
}

func TestRenderUnitNoArgs(t *testing.T) {
	out, err := renderUnit(DefaultUnit, Spec{Description: "x", BinaryPath: "/p/sf"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "ExecStart=/p/sf\n") {
		t.Fatalf("ExecStart should have no trailing args\n%s", out)
	}
}

func TestRenderUnitQuotesSpaces(t *testing.T) {
	out, err := renderUnit(DefaultUnit, Spec{
		Description: "x",
		BinaryPath:  "/path with space/sf",
		Args:        []string{"-config", "/tmp/a b.yaml"},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := "ExecStart='/path with space/sf' -config '/tmp/a b.yaml'"
	if !strings.Contains(out, want) {
		t.Fatalf("rendered unit missing quoted ExecStart %q\n%s", want, out)
	}
}

func TestServiceUnitName(t *testing.T) {
	if got := serviceUnitName("skillfleet-agent"); got != "skillfleet-agent.service" {
		t.Fatalf("serviceUnitName without suffix = %q", got)
	}
	if got := serviceUnitName("skillfleet-agent.service"); got != "skillfleet-agent.service" {
		t.Fatalf("serviceUnitName with suffix = %q", got)
	}
}

func TestAgentSpec(t *testing.T) {
	s := AgentSpec("/usr/local/bin/skillfleet-agent", "/tmp/agent.json")
	if s.Name != "skillfleet-agent" {
		t.Fatalf("Name = %q", s.Name)
	}
	if s.BinaryPath != "/usr/local/bin/skillfleet-agent" {
		t.Fatalf("BinaryPath = %q", s.BinaryPath)
	}
	if len(s.Args) != 2 || s.Args[0] != "-config" || s.Args[1] != "/tmp/agent.json" {
		t.Fatalf("Args = %#v", s.Args)
	}
}

func TestServerSpecWithoutConfig(t *testing.T) {
	s := ServerSpec("/usr/local/bin/skillfleet-server", "")
	if s.Name != "skillfleet-server" {
		t.Fatalf("Name = %q", s.Name)
	}
	if len(s.Args) != 0 {
		t.Fatalf("Args = %#v", s.Args)
	}
}

func TestLaunchdPlistPlaceholder(t *testing.T) {
	if got := LaunchdPlist(Spec{}); got != "" {
		t.Fatalf("LaunchdPlist = %q", got)
	}
}
