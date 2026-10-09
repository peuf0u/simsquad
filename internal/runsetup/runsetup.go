// Package runsetup turns a feature file and a repo's equipment into a test
// run: it picks the squad and platforms, refuses what can't run (unequipped
// platforms, a squad busy with an unfinished run), computes the per-worker
// deadline so the agent never does that arithmetic, and creates the run
// folder `.simsquad/runs/<run-id>/` with run.json and a copy of the feature
// file. It is the logic behind `simsquad run new`.
package runsetup

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/peuf0u/simsquad/internal/config"
	"github.com/peuf0u/simsquad/internal/contract"
	"github.com/peuf0u/simsquad/internal/feature"
	"github.com/peuf0u/simsquad/internal/util"
)

// Deadline formula, in seconds. A worker gets a fixed allowance for start-up
// and writing its result, then per scenario an allowance for the app reset
// plus each step, then the exploration timebox. Pilot used a flat
// `timebox + 2 min`; feature files have no timebox yet (`@timebox:` is out of
// scope for 0.4.0), so the scripted part is sized from the steps instead.
const (
	DeadlineBaseSeconds        = 120
	DeadlinePerScenarioSeconds = 60
	DeadlinePerStepSeconds     = 30
	DeadlineExplorationSeconds = 600
)

// RunsDir is the run folder root, relative to the app repo.
var RunsDir = filepath.Join(".simsquad", "runs")

// Options configures New.
type Options struct {
	// FeaturePath is the feature file as given on the command line.
	FeaturePath string
	// Fresh gives the run its own uniquely named squad.
	Fresh bool
}

// New sets up a test run in the repo found from the working directory and
// returns its record and run folder.
func New(opts Options) (contract.RunRecord, string, error) {
	src, err := os.ReadFile(opts.FeaturePath)
	if err != nil {
		return contract.RunRecord{}, "", fmt.Errorf("read feature file: %w", err)
	}
	feat, err := feature.Parse(src, opts.FeaturePath)
	if err != nil {
		return contract.RunRecord{}, "", err
	}

	configDir := config.FindConfigDir(".")
	if configDir == "" {
		return contract.RunRecord{}, "", errors.New("no simsquad.toml found; equip this repo first with `simsquad equip`")
	}
	cfg, err := config.Load(configDir)
	if err != nil {
		return contract.RunRecord{}, "", err
	}
	equipped := equippedPlatforms(cfg)
	if len(equipped) == 0 {
		return contract.RunRecord{}, "", errors.New("no devices equipped; add iOS or Android devices with `simsquad equip`")
	}

	rec := contract.RunRecord{
		FeatureTitle: feat.Title,
		FeatureFile:  opts.FeaturePath,
		Source:       feat.Source,
		Fresh:        opts.Fresh,
		WorkerModel:  cfg.AgentWorkerModel(),
		Scenarios:    []contract.RunScenario{},
	}
	for _, s := range feat.Scenarios {
		platforms, err := resolvePlatforms(s, equipped)
		if err != nil {
			return contract.RunRecord{}, "", err
		}
		rec.Scenarios = append(rec.Scenarios, contract.RunScenario{
			Name: s.Name, Steps: s.Steps, Tags: s.Tags, Platforms: platforms,
		})
	}
	if e := feat.Exploration; e != nil {
		platforms, err := resolvePlatforms(*e, equipped)
		if err != nil {
			return contract.RunRecord{}, "", err
		}
		rec.Exploration = &contract.RunExploration{
			Name: e.Name, Charter: e.Description, Steps: e.Steps, Tags: e.Tags, Platforms: platforms,
		}
	}
	rec.Platforms = runPlatforms(rec)
	rec.DeadlineSeconds = deadline(rec)

	squad := cfg.AgentTestSquad()
	if squad == "" {
		squad = "qa-" + filepath.Base(configDir)
	}
	squad = util.SanitiseName(squad)
	runsRoot := filepath.Join(configDir, RunsDir)
	if opts.Fresh {
		squad += "-" + randomHex(3)
	} else if err := refuseUnfinished(runsRoot, squad); err != nil {
		return contract.RunRecord{}, "", err
	}
	rec.SquadName = squad

	now := util.UTCNow()
	rec.CreatedAt = util.ISO(now)
	runDir, runID, err := makeRunDir(runsRoot, now, feat.Title)
	if err != nil {
		return contract.RunRecord{}, "", err
	}
	rec.RunID = runID
	if err := os.WriteFile(filepath.Join(runDir, contract.RunFeatureFile), src, 0o644); err != nil {
		return contract.RunRecord{}, "", fmt.Errorf("copy feature file: %w", err)
	}
	if err := util.WriteJSON(filepath.Join(runDir, contract.RunRecordFile), rec); err != nil {
		return contract.RunRecord{}, "", err
	}
	return rec, runDir, nil
}

func equippedPlatforms(cfg *config.Config) []string {
	var out []string
	if len(cfg.IOSSpecs()) > 0 {
		out = append(out, feature.PlatformIOS)
	}
	if len(cfg.AndroidSpecs()) > 0 {
		out = append(out, feature.PlatformAndroid)
	}
	return out
}

// resolvePlatforms applies a scenario's platform tags to the equipment. A
// tag naming a platform the equipment lacks is refused, so a missing device
// becomes an explicit setup step instead of a silently skipped scenario.
func resolvePlatforms(s feature.Scenario, equipped []string) ([]string, error) {
	if len(s.Platforms) == 0 {
		return equipped, nil
	}
	for _, p := range s.Platforms {
		if !contains(equipped, p) {
			return nil, fmt.Errorf("scenario %q is tagged @%s but this repo has no %s equipment; add %s devices with `simsquad equip`", s.Name, p, displayName(p), displayName(p))
		}
	}
	return s.Platforms, nil
}

// runPlatforms is the union of the scenarios' platforms in canonical order.
func runPlatforms(rec contract.RunRecord) []string {
	used := map[string]bool{}
	for _, s := range rec.Scenarios {
		for _, p := range s.Platforms {
			used[p] = true
		}
	}
	if rec.Exploration != nil {
		for _, p := range rec.Exploration.Platforms {
			used[p] = true
		}
	}
	var out []string
	for _, p := range []string{feature.PlatformIOS, feature.PlatformAndroid} {
		if used[p] {
			out = append(out, p)
		}
	}
	return out
}

// deadline sizes the busiest platform's worker; every worker gets the same
// limit so the agent passes one number to `run worker`.
func deadline(rec contract.RunRecord) int {
	best := 0
	for _, p := range rec.Platforms {
		total := DeadlineBaseSeconds
		for _, s := range rec.Scenarios {
			if contains(s.Platforms, p) {
				total += DeadlinePerScenarioSeconds + DeadlinePerStepSeconds*len(s.Steps)
			}
		}
		if rec.Exploration != nil && contains(rec.Exploration.Platforms, p) {
			total += DeadlineExplorationSeconds
		}
		best = max(best, total)
	}
	return best
}

// refuseUnfinished rejects a run on a squad that an earlier run in this repo
// still uses: the two would reset each other's app state.
func refuseUnfinished(runsRoot, squad string) error {
	entries, err := os.ReadDir(runsRoot)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, e := range entries {
		dir := filepath.Join(runsRoot, e.Name())
		var other contract.RunRecord
		if err := util.ReadJSON(filepath.Join(dir, contract.RunRecordFile), &other); err != nil {
			continue
		}
		if other.SquadName != squad {
			continue
		}
		if _, err := os.Stat(filepath.Join(dir, contract.RunReportFile)); err == nil {
			continue
		}
		return fmt.Errorf("squad %s is in use by unfinished run %s (no %s yet); finish it with `simsquad run report %s`, or pass --fresh", squad, other.RunID, contract.RunReportFile, dir)
	}
	return nil
}

// makeRunDir creates `<runsRoot>/<UTC timestamp>-<feature slug>`, adding a
// counter if a run started in the same second already took the name.
func makeRunDir(runsRoot string, now time.Time, title string) (string, string, error) {
	if err := os.MkdirAll(runsRoot, 0o755); err != nil {
		return "", "", err
	}
	slug := util.SanitiseName(title)
	if len(slug) > 40 {
		slug = strings.TrimRight(slug[:40], "-")
	}
	base := now.UTC().Format("20060102-150405") + "-" + slug
	for i := 1; ; i++ {
		id := base
		if i > 1 {
			id = fmt.Sprintf("%s-%d", base, i)
		}
		dir := filepath.Join(runsRoot, id)
		err := os.Mkdir(dir, 0o755)
		if err == nil {
			return dir, id, nil
		}
		if !errors.Is(err, fs.ErrExist) {
			return "", "", err
		}
	}
}

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

func displayName(platform string) string {
	if platform == feature.PlatformIOS {
		return "iOS"
	}
	return "Android"
}
