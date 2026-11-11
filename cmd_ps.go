package main

import (
	"encoding/json"
	"fmt"
	"io"
	"text/tabwriter"
	"time"

	"github.com/guangshinhaha/shinfactory/cmdmeta"
	"github.com/guangshinhaha/shinfactory/internal/loopreg"
	"github.com/spf13/cobra"
)

func newPsCmd(_ *App) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "ps",
		Short: cmdmeta.Short("ps"),
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			dir, err := loopreg.DefaultDir()
			if err != nil {
				return err
			}
			return runPs(cmd.OutOrStdout(), dir, asJSON, time.Now())
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print the registered loops as JSON")
	return cmd
}

// runPs lists the loops registered in dir. It reads only the registry, so it
// works from any directory.
func runPs(w io.Writer, dir string, asJSON bool, now time.Time) error {
	loops, err := loopreg.List(dir)
	if err != nil {
		return err
	}
	if asJSON {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(loops)
	}
	if len(loops) == 0 {
		_, err := fmt.Fprintln(w, "no loops registered")
		return err
	}
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "REPO\tPORT\tPID\tSTATE\tMODE\tSLOTS\tNEEDS YOU\tVERSION\tUPDATED\tPROJECT")
	for _, e := range loops {
		state := e.LoopState
		if e.Stale {
			state = "stale"
		}
		fmt.Fprintf(tw, "%s\t%d\t%d\t%s\t%s\t%d/%d\t%d\t%s\t%s\t%s\n",
			e.Repo, e.Port, e.PID, state, e.Mode, e.SlotsBusy, e.SlotsMax, e.NeedsYou,
			e.Version, ago(now, e.UpdatedAt), e.ProjectDir)
	}
	return tw.Flush()
}

func ago(now, then time.Time) string {
	if then.IsZero() {
		return "-"
	}
	d := now.Sub(then).Round(time.Second)
	if d < 0 {
		d = 0
	}
	return d.String() + " ago"
}
