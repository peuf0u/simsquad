// RenderReport turns a report into report.md, readable and pasteable into a
// PR. It is the Go port of simsquad-pilot's scripts/render.py, plus the
// source link, the env and the scenario × device matrix.

package agenttest

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/peuf0u/simsquad/internal/contract"
)

var verdictLabel = map[string]string{
	contract.VerdictPassed: "✅ PASSED",
	contract.VerdictFailed: "❌ FAILED",
	contract.VerdictInfra:  "⚠️ INFRA FAILURE",
}

var cellLabel = map[string]string{
	contract.ScenarioPassed:  "✅ passed",
	contract.ScenarioFailed:  "❌ failed",
	contract.ScenarioBlocked: "⚠️ blocked",
	contract.MatrixSkipped:   "– skipped",
}

var scenarioMark = map[string]string{
	contract.ScenarioPassed:  "✓",
	contract.ScenarioFailed:  "✗",
	contract.ScenarioBlocked: "→",
}

var severityOrder = map[string]int{"high": 0, "medium": 1, "low": 2}

// RenderReport renders rep as Markdown. Paths stay relative to the run dir,
// where report.md is written, so evidence images resolve.
func RenderReport(rep *contract.Report) string {
	var b strings.Builder
	line := func(format string, args ...any) { fmt.Fprintf(&b, format+"\n", args...) }
	c := rep.Summary.Counts

	title := rep.FeatureTitle
	if title == "" {
		title = rep.RunID
	}
	line("# %s — %s", title, verdictLabel[rep.Summary.Verdict])
	line("")
	if rep.Source != "" {
		line("Source: %s", sourceLink(rep.Source))
		line("")
	}
	line("Run `%s` · squad `%s` · %s → %s", rep.RunID, rep.Squad.Name, rep.StartedAt, rep.FinishedAt)
	line("")
	if rep.Summary.Reason != "" {
		label := "Reason"
		if rep.Summary.Verdict == contract.VerdictInfra {
			label = "Infra"
		}
		line("**%s:** %s", label, rep.Summary.Reason)
		line("")
	}
	line("**%d device(s)** · scenarios %d passed / %d failed / %d blocked · **%d bug(s)** · %d question(s) · %d note(s)",
		c.Devices, c.ScenariosPassed, c.ScenariosFailed, c.ScenariosBlocked, c.Bugs, c.Questions, c.Notes)

	renderEnv(line, rep.Squad.Env)
	renderMatrix(line, rep)

	type item struct {
		label string
		f     contract.WorkerFinding
	}
	var bugs, others []item
	for _, d := range rep.Devices {
		for _, f := range d.Findings {
			if f.Type == contract.FindingBug {
				bugs = append(bugs, item{deviceLabel(d), f})
			} else {
				others = append(others, item{deviceLabel(d), f})
			}
		}
	}
	if len(bugs) > 0 {
		sort.SliceStable(bugs, func(i, j int) bool {
			si, sj := severityRank(bugs[i].f.Severity), severityRank(bugs[j].f.Severity)
			if si != sj {
				return si < sj
			}
			return bugs[i].f.Title < bugs[j].f.Title
		})
		line("")
		line("## Bugs")
		for _, it := range bugs {
			f := it.f
			sev := f.Severity
			if sev == "" {
				sev = "?"
			}
			line("")
			line("### [%s] %s", sev, f.Title)
			line("")
			line("*Device: %s*", it.label)
			if f.Expected != "" || f.Actual != "" {
				line("")
				line("- **Expected:** %s", orDash(f.Expected))
				line("- **Actual:** %s", orDash(f.Actual))
			}
			if len(f.ReproSteps) > 0 {
				line("")
				line("Repro:")
				for i, s := range f.ReproSteps {
					line("%d. %s", i+1, s)
				}
			}
			for _, shot := range f.Evidence {
				line("")
				line("![evidence](%s)", shot)
			}
		}
	}

	line("")
	line("## Devices")
	for _, d := range rep.Devices {
		line("")
		line("### %s — %s (`%s`)", orPlatform(d.Model, d.Platform), d.OSVersion, d.ID)
		line("")
		expl := "—"
		if d.Exploration != nil {
			expl = d.Exploration.Status
		}
		line("Worker: %s · exploration: %s", d.WorkerStatus, expl)
		if d.WorkerReason != "" {
			line("  — %s", d.WorkerReason)
		}
		if d.Exploration != nil && d.Exploration.BlockedReason != "" {
			line("  — exploration: %s", d.Exploration.BlockedReason)
		}
		if len(d.ValidationErrors) > 0 {
			line("")
			line("Invalid result.json:")
			for _, e := range d.ValidationErrors {
				line("- `%s`", e)
			}
		}
		if len(d.Scenarios) > 0 {
			line("")
			for _, s := range d.Scenarios {
				l := fmt.Sprintf("- %s %s", scenarioMark[s.Status], s.Name)
				if s.Observation != "" {
					l += " — " + s.Observation
				}
				line("%s", l)
			}
		}
	}

	if len(others) > 0 {
		line("")
		line("## Questions and notes")
		for _, it := range others {
			line("")
			line("- **%s** · %s *(%s)*", it.f.Type, it.f.Title, it.label)
			for _, shot := range it.f.Evidence {
				line("  - evidence: %s", shot)
			}
		}
	}

	if len(rep.Undeployed) > 0 {
		line("")
		line("## Not deployed")
		line("")
		for _, u := range rep.Undeployed {
			l := fmt.Sprintf("- %s `%s`: %s", u.Platform, u.Name, u.Status)
			if u.Error != "" {
				l += " — " + u.Error
			}
			line("%s", l)
		}
	}

	line("")
	line("## Artifacts")
	line("")
	line("Per-device `result.json`, `worker.log` and `screenshots/` sit under")
	line("`workers/<device-id>/` next to this report in the run directory.")
	return b.String()
}

// renderEnv shows the squad env keys and values exactly as deployed, so the
// reader sees which account and stage were tested.
func renderEnv(line func(string, ...any), env map[string]string) {
	line("")
	line("## Env")
	line("")
	if len(env) == 0 {
		line("_No env._")
		return
	}
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	line("| Key | Value |")
	line("|---|---|")
	for _, k := range keys {
		line("| `%s` | %s |", k, cell(env[k]))
	}
}

// renderMatrix draws the scenario × device table.
func renderMatrix(line func(string, ...any), rep *contract.Report) {
	if len(rep.Matrix) == 0 || len(rep.Devices) == 0 {
		return
	}
	line("")
	line("## Results")
	line("")
	head := "| Scenario |"
	sep := "|---|"
	for _, d := range rep.Devices {
		head += " " + cell(deviceLabel(d)) + " |"
		sep += "---|"
	}
	line("%s", head)
	line("%s", sep)
	for _, row := range rep.Matrix {
		name := row.Scenario
		if row.Exploration {
			name += " (exploration)"
		}
		r := "| " + cell(name) + " |"
		for _, d := range rep.Devices {
			r += " " + cellLabel[row.Results[d.ID]] + " |"
		}
		line("%s", r)
	}
}

// sourceLink renders a URL source as a link; a ticket key or free text is
// shown as written.
func sourceLink(src string) string {
	if strings.HasPrefix(src, "http://") || strings.HasPrefix(src, "https://") {
		return "[" + src + "](" + src + ")"
	}
	return src
}

// cell makes a value safe inside a Markdown table cell without changing
// what it reads as: pipes are escaped, line breaks become <br>.
func cell(s string) string {
	s = strings.ReplaceAll(s, "|", `\|`)
	s = strings.ReplaceAll(s, "\r\n", "<br>")
	return strings.ReplaceAll(s, "\n", "<br>")
}

func deviceLabel(d contract.ReportDevice) string {
	return fmt.Sprintf("%s (%s)", orPlatform(d.Model, d.Platform), d.ID)
}

func orPlatform(model, platform string) string {
	if model != "" {
		return model
	}
	return platform
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

func severityRank(s string) int {
	if r, ok := severityOrder[s]; ok {
		return r
	}
	return len(severityOrder)
}
