package daemon

import "strings"

func windowsTaskName(name string) string {
	base := strings.TrimPrefix(name, "skillfleet-")
	if base == "" {
		base = name
	}
	return "SkillFleet-" + base
}

func createXMLArgs(name, xmlPath string) []string {
	return []string{
		"/create",
		"/tn", windowsTaskName(name),
		"/xml", xmlPath,
		"/f",
	}
}

func runArgs(name string) []string {
	return []string{"/run", "/tn", windowsTaskName(name)}
}

func endArgs(name string) []string {
	return []string{"/end", "/tn", windowsTaskName(name)}
}

func queryArgs(name string) []string {
	return []string{"/query", "/tn", windowsTaskName(name), "/fo", "list", "/v"}
}

func deleteArgs(name string) []string {
	return []string{"/delete", "/tn", windowsTaskName(name), "/f"}
}

func taskXML(s Spec, userID string) string {
	args := strings.Join(windowsQuoteArgs(s.Args), " ")
	return `<?xml version="1.0" encoding="UTF-8"?>
<Task version="1.4" xmlns="http://schemas.microsoft.com/windows/2004/02/mit/task">
  <RegistrationInfo>
    <Description>` + xmlEscape(s.Description) + `</Description>
  </RegistrationInfo>
  <Triggers>
    <LogonTrigger>
      <Enabled>true</Enabled>
    </LogonTrigger>
  </Triggers>
  <Principals>
    <Principal id="Author">
      <UserId>` + xmlEscape(userID) + `</UserId>
      <LogonType>InteractiveToken</LogonType>
      <RunLevel>LeastPrivilege</RunLevel>
    </Principal>
  </Principals>
  <Settings>
    <MultipleInstancesPolicy>IgnoreNew</MultipleInstancesPolicy>
    <DisallowStartIfOnBatteries>false</DisallowStartIfOnBatteries>
    <StopIfGoingOnBatteries>false</StopIfGoingOnBatteries>
    <AllowHardTerminate>true</AllowHardTerminate>
    <StartWhenAvailable>true</StartWhenAvailable>
    <RunOnlyIfNetworkAvailable>false</RunOnlyIfNetworkAvailable>
    <IdleSettings>
      <StopOnIdleEnd>false</StopOnIdleEnd>
      <RestartOnIdle>false</RestartOnIdle>
    </IdleSettings>
    <AllowStartOnDemand>true</AllowStartOnDemand>
    <Enabled>true</Enabled>
    <Hidden>true</Hidden>
    <RunOnlyIfIdle>false</RunOnlyIfIdle>
    <WakeToRun>false</WakeToRun>
    <ExecutionTimeLimit>PT0S</ExecutionTimeLimit>
    <Priority>7</Priority>
  </Settings>
  <Actions Context="Author">
    <Exec>
      <Command>` + xmlEscape(s.BinaryPath) + `</Command>
      <Arguments>` + xmlEscape(args) + `</Arguments>
    </Exec>
  </Actions>
</Task>
`
}

func windowsQuoteArgs(args []string) []string {
	quoted := make([]string, 0, len(args))
	for _, arg := range args {
		quoted = append(quoted, windowsQuoteArg(arg))
	}
	return quoted
}

func windowsCommandLine(s Spec) string {
	parts := []string{windowsQuoteArg(s.BinaryPath)}
	parts = append(parts, windowsQuoteArgs(s.Args)...)
	return strings.Join(parts, " ")
}

func windowsQuoteArg(s string) string {
	if s == "" {
		return `""`
	}
	needsQuote := strings.ContainsAny(s, " \t\n\v\"")
	if !needsQuote {
		return s
	}
	var b strings.Builder
	b.WriteByte('"')
	backslashes := 0
	for _, r := range s {
		switch r {
		case '\\':
			backslashes++
		case '"':
			b.WriteString(strings.Repeat(`\`, backslashes*2+1))
			b.WriteRune(r)
			backslashes = 0
		default:
			if backslashes > 0 {
				b.WriteString(strings.Repeat(`\`, backslashes))
				backslashes = 0
			}
			b.WriteRune(r)
		}
	}
	if backslashes > 0 {
		b.WriteString(strings.Repeat(`\`, backslashes*2))
	}
	b.WriteByte('"')
	return b.String()
}

func xmlEscape(s string) string {
	repl := strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
		"\"", "&quot;",
		"'", "&apos;",
	)
	return repl.Replace(s)
}

func parseTaskQuery(st *Status, out string) {
	for _, line := range strings.Split(out, "\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		switch strings.ToLower(key) {
		case "taskname", "task name":
			st.UnitPath = value
		case "status":
			st.ActiveState = strings.ToLower(strings.ReplaceAll(value, " ", "_"))
		}
	}
}
