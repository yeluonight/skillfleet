package daemon

import (
	"reflect"
	"strings"
	"testing"
)

func TestWindowsTaskName(t *testing.T) {
	if got := windowsTaskName("skillfleet-agent"); got != `SkillFleet-agent` {
		t.Fatalf("windowsTaskName(agent) = %q", got)
	}
	if got := windowsTaskName("custom"); got != `SkillFleet-custom` {
		t.Fatalf("windowsTaskName(custom) = %q", got)
	}
}

func TestCreateXMLArgs(t *testing.T) {
	want := []string{"/create", "/tn", `SkillFleet-agent`, "/xml", `C:\Temp\skillfleet-agent.task.xml`, "/f"}
	if got := createXMLArgs("skillfleet-agent", `C:\Temp\skillfleet-agent.task.xml`); !reflect.DeepEqual(got, want) {
		t.Fatalf("createXMLArgs() = %#v\nwant %#v", got, want)
	}
}

func TestTaskXML(t *testing.T) {
	s := Spec{
		Name:        "skillfleet-agent",
		Description: "SkillFleet <agent>",
		BinaryPath:  `C:\Users\Me\Programs\skillfleet\skillfleet-agent.exe`,
		Args:        []string{"-foreground", "-config", `C:\Users\Me\App Data\agent.json`},
	}
	got := taskXML(s, `DESKTOP\Me`)
	for _, want := range []string{
		`<Hidden>true</Hidden>`,
		`<LogonType>InteractiveToken</LogonType>`,
		`<RunLevel>LeastPrivilege</RunLevel>`,
		`<Description>SkillFleet &lt;agent&gt;</Description>`,
		`<UserId>DESKTOP\Me</UserId>`,
		`<Command>C:\Users\Me\Programs\skillfleet\skillfleet-agent.exe</Command>`,
		`<Arguments>-foreground -config &quot;C:\Users\Me\App Data\agent.json&quot;</Arguments>`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("taskXML() missing %q\n%s", want, got)
		}
	}
}

func TestTaskControlArgs(t *testing.T) {
	cases := []struct {
		name string
		got  []string
		want []string
	}{
		{"run", runArgs("skillfleet-server"), []string{"/run", "/tn", `SkillFleet-server`}},
		{"end", endArgs("skillfleet-server"), []string{"/end", "/tn", `SkillFleet-server`}},
		{"query", queryArgs("skillfleet-server"), []string{"/query", "/tn", `SkillFleet-server`, "/fo", "list", "/v"}},
		{"delete", deleteArgs("skillfleet-server"), []string{"/delete", "/tn", `SkillFleet-server`, "/f"}},
	}
	for _, tc := range cases {
		if !reflect.DeepEqual(tc.got, tc.want) {
			t.Fatalf("%s args = %#v, want %#v", tc.name, tc.got, tc.want)
		}
	}
}

func TestParseTaskQuery(t *testing.T) {
	var st Status
	parseTaskQuery(&st, "TaskName: SkillFleet-agent\r\nStatus: Running\r\n")
	if st.UnitPath != `SkillFleet-agent` {
		t.Fatalf("UnitPath = %q", st.UnitPath)
	}
	if st.ActiveState != "running" {
		t.Fatalf("ActiveState = %q", st.ActiveState)
	}
}
