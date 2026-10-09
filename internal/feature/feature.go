// Package feature reads Gherkin feature files into the scenarios a test run
// executes. It wraps the official Cucumber Gherkin parser: Background steps
// are prepended to every scenario, Scenario Outlines expand into one scenario
// per Examples row, and tags are inherited from feature and rule. Only
// `@ios`, `@android` and `@explore` mean anything here; every other tag is
// kept and ignored, never rejected (ADR 0003), so feature files written for
// future capabilities still run.
package feature

import (
	"bytes"
	"errors"
	"fmt"
	"strings"

	gherkin "github.com/cucumber/gherkin/go/v42"
	messages "github.com/cucumber/messages/go/v34"
)

// Platform tags and the exploration tag.
const (
	TagIOS     = "@ios"
	TagAndroid = "@android"
	TagExplore = "@explore"
)

// Platform names as used across the JSON contract.
const (
	PlatformIOS     = "ios"
	PlatformAndroid = "android"
)

// Feature is a parsed feature file.
type Feature struct {
	Title string
	// Source is the value of the description's `Source:` line, or "".
	Source string
	// Scenarios are the scripted scenarios, in file order.
	Scenarios []Scenario
	// Exploration is the `@explore` scenario, or nil.
	Exploration *Scenario
}

// Scenario is one executable scenario (an Outline row counts as one).
type Scenario struct {
	Name string
	// Description is the scenario's free text; for an exploration it is the
	// charter.
	Description string
	Steps       []string
	Tags        []string
	// Platforms is the restriction the `@ios`/`@android` tags express, in
	// canonical order; nil means any platform.
	Platforms []string
}

// Parse reads a feature file. Malformed Gherkin is returned as an error that
// carries the parser's `(line:column): message`.
func Parse(src []byte, uri string) (*Feature, error) {
	newID := (&messages.Incrementing{}).NewId
	doc, err := gherkin.ParseGherkinDocument(bytes.NewReader(src), newID)
	if err != nil {
		return nil, fmt.Errorf("%s: %s", uri, strings.TrimSpace(err.Error()))
	}
	if doc.Feature == nil {
		return nil, fmt.Errorf("%s: no Feature found", uri)
	}
	scenarios, steps := index(doc.Feature)
	f := &Feature{
		Title:  strings.TrimSpace(doc.Feature.Name),
		Source: sourceLine(doc.Feature.Description),
	}
	for _, p := range gherkin.Pickles(*doc, uri, newID) {
		sc := scenarios[p.AstNodeIds[0]]
		s := Scenario{
			Name:        p.Name,
			Description: dedent(sc.Description),
			Steps:       renderSteps(p.Steps, steps),
			Tags:        tagNames(p.Tags),
		}
		s.Platforms = platformsOf(s.Tags)
		if !hasTag(s.Tags, TagExplore) {
			f.Scenarios = append(f.Scenarios, s)
			continue
		}
		if f.Exploration != nil {
			return nil, fmt.Errorf("%s: more than one @explore scenario (%q and %q); a feature has at most one exploration", uri, f.Exploration.Name, s.Name)
		}
		f.Exploration = &s
	}
	if len(f.Scenarios) == 0 && f.Exploration == nil {
		return nil, errors.New(uri + ": feature has no scenarios")
	}
	return f, nil
}

// index maps AST ids to scenarios and steps, which Pickles flattens away
// (it drops scenario descriptions and step keywords).
func index(feat *messages.Feature) (map[string]*messages.Scenario, map[string]*messages.Step) {
	scenarios := map[string]*messages.Scenario{}
	steps := map[string]*messages.Step{}
	addBackground := func(b *messages.Background) {
		for _, st := range b.Steps {
			steps[st.Id] = st
		}
	}
	addScenario := func(s *messages.Scenario) {
		scenarios[s.Id] = s
		for _, st := range s.Steps {
			steps[st.Id] = st
		}
	}
	for _, c := range feat.Children {
		switch {
		case c.Background != nil:
			addBackground(c.Background)
		case c.Scenario != nil:
			addScenario(c.Scenario)
		case c.Rule != nil:
			for _, rc := range c.Rule.Children {
				if rc.Background != nil {
					addBackground(rc.Background)
				}
				if rc.Scenario != nil {
					addScenario(rc.Scenario)
				}
			}
		}
	}
	return scenarios, steps
}

func renderSteps(ps []*messages.PickleStep, steps map[string]*messages.Step) []string {
	out := make([]string, 0, len(ps))
	for _, p := range ps {
		line := p.Text
		if st := steps[p.AstNodeIds[0]]; st != nil {
			line = strings.TrimSpace(st.Keyword) + " " + p.Text
		}
		if arg := p.Argument; arg != nil {
			if arg.DocString != nil {
				line += "\n" + arg.DocString.Content
			}
			if arg.DataTable != nil {
				for _, row := range arg.DataTable.Rows {
					cells := make([]string, len(row.Cells))
					for i, c := range row.Cells {
						cells[i] = c.Value
					}
					line += "\n| " + strings.Join(cells, " | ") + " |"
				}
			}
		}
		out = append(out, line)
	}
	return out
}

func tagNames(tags []*messages.PickleTag) []string {
	out := make([]string, 0, len(tags))
	for _, t := range tags {
		out = append(out, t.Name)
	}
	return out
}

func hasTag(tags []string, want string) bool {
	for _, t := range tags {
		if strings.EqualFold(t, want) {
			return true
		}
	}
	return false
}

func platformsOf(tags []string) []string {
	var out []string
	if hasTag(tags, TagIOS) {
		out = append(out, PlatformIOS)
	}
	if hasTag(tags, TagAndroid) {
		out = append(out, PlatformAndroid)
	}
	return out
}

// sourceLine finds the `Source:` line in a feature description.
func sourceLine(desc string) string {
	for _, line := range strings.Split(desc, "\n") {
		line = strings.TrimSpace(line)
		if len(line) > len("source:") && strings.EqualFold(line[:len("source:")], "source:") {
			return strings.TrimSpace(line[len("source:"):])
		}
	}
	return ""
}

// dedent strips the indentation Gherkin keeps on description lines.
func dedent(desc string) string {
	lines := strings.Split(desc, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimSpace(l)
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}
