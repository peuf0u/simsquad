package cli

import (
	"github.com/spf13/cobra"

	"github.com/peuf0u/simsquad/internal/contract"
	"github.com/peuf0u/simsquad/internal/runsetup"
)

func newRunNewCmd() *cobra.Command {
	var (
		fresh         bool
		worker, model string
	)
	cmd := &cobra.Command{
		Use:   "new <feature-file>",
		Short: "Start a test run from a Gherkin feature file",
		Long: "Parses the feature file, picks the squad and platforms from the\n" +
			"equipment, computes the per-worker deadline and creates\n" +
			".simsquad/runs/<run-id>/ with run.json and a copy of the feature file.\n" +
			"Run it from inside the app repo. Refuses unequipped platform tags,\n" +
			"malformed Gherkin, and a squad in use by an unfinished run that\n" +
			"shares a platform with this one.\n\n" +
			"Every run names its worker model: --model, else [agent].worker_model\n" +
			"(simsquad.local.toml over simsquad.toml), else the default for\n" +
			"--worker: sonnet for claude, gpt-6.1-sol for codex.\n\n" +
			"  simsquad run new qa/features/login.feature > run.json\n" +
			"  simsquad run new --worker codex qa/features/login.feature > run.json",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			rec, runDir, err := runsetup.New(runsetup.Options{
				FeaturePath: args[0], Fresh: fresh, Worker: worker, Model: model,
			})
			if err != nil {
				return err
			}
			return writeJSON(cmd, contract.RunNewOutput{
				RunID:           rec.RunID,
				RunDir:          runDir,
				SquadName:       rec.SquadName,
				Fresh:           rec.Fresh,
				Platforms:       rec.Platforms,
				DeadlineSeconds: rec.DeadlineSeconds,
				Worker:          rec.Worker,
				WorkerModel:     rec.WorkerModel,
				AppNotes:        rec.AppNotes,
			})
		},
	}
	cmd.Flags().BoolVar(&fresh, "fresh", false, "use a unique, isolated squad for this run (dismiss it afterwards)")
	cmd.Flags().StringVar(&worker, "worker", runsetup.DefaultWorker, "worker CLI: claude or codex (picks the default model)")
	cmd.Flags().StringVar(&model, "model", "", "worker model for this run (overrides [agent].worker_model and the --worker default)")
	return cmd
}
