package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/guangshinhaha/shinfactory/adapter"
	"github.com/guangshinhaha/shinfactory/cmdmeta"
	"github.com/guangshinhaha/shinfactory/internal/linestatus"
	"github.com/guangshinhaha/shinfactory/internal/orderx"
	"github.com/guangshinhaha/shinfactory/internal/snapshot"
	"github.com/guangshinhaha/shinfactory/internal/statusfile"
	"github.com/spf13/cobra"
)

type statusSummary struct {
	ActiveCooks int
	OrdersDepth int
	LoopState   string
}

func newStatusCmd(app *App) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "status",
		Short: cmdmeta.Short("status"),
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if asJSON {
				return runStatusJSON(app, cmd.OutOrStdout())
			}
			return runStatus(app)
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print the line's full state as JSON: mode, concurrency, running, queued and parked orders, needs-shin")
	return cmd
}

// runStatusJSON prints the line's state for a coordinator to parse.
func runStatusJSON(app *App, w io.Writer) error {
	runtimeDir, err := app.RuntimeDir()
	if err != nil {
		return err
	}
	report, err := linestatus.Read(runtimeDir)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), stoppedTimeout)
	defer cancel()
	stopped, listed, err := app.backlogStopped(ctx)
	if err != nil {
		return err
	}
	if listed {
		report.MergeNeedsShin(stopped)
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(report)
}

// stoppedTimeout bounds the backlog call so a hung gh cannot hang status.
const stoppedTimeout = 30 * time.Second

// backlogStopped asks the backlog adapter which items wait on a person. A
// backlog with no `stopped` script reports listed=false and the recorded stops stand; a script that fails is
// an error, because an empty needs_shin would read as "nobody is waiting".
func (a *App) backlogStopped(ctx context.Context) (items []linestatus.NeedsShin, listed bool, _ error) {
	projectDir, err := a.ProjectDir()
	if err != nil {
		return nil, false, err
	}
	if strings.TrimSpace(a.Config.Adapters["backlog"].Scripts["stopped"]) == "" {
		return nil, false, nil
	}
	out, err := adapter.NewRunner(projectDir, a.Config).Run(ctx, "backlog", "stopped", adapter.RunOptions{})
	if err != nil {
		return nil, false, fmt.Errorf("list stopped backlog items: %w", err)
	}
	dec := json.NewDecoder(strings.NewReader(out))
	for dec.More() {
		var row struct {
			ID    string `json:"id"`
			Title string `json:"title"`
			URL   string `json:"url"`
		}
		if err := dec.Decode(&row); err != nil {
			return nil, false, fmt.Errorf("parse stopped backlog items: %w", err)
		}
		items = append(items, linestatus.NeedsShin{Order: row.ID, Title: row.Title, URL: row.URL})
	}
	return items, true, nil
}

func runStatus(app *App) error {
	runtimeDir, err := app.RuntimeDir()
	if err != nil {
		return err
	}
	summary, err := readStatusSummary(runtimeDir)
	if err != nil {
		return err
	}

	if summary.ActiveCooks == 0 {
		fmt.Fprintf(
			os.Stdout,
			"no active cooks | orders=%d | loop=%s\n",
			summary.OrdersDepth,
			summary.LoopState,
		)
	} else {
		fmt.Fprintf(
			os.Stdout,
			"active cooks=%d | orders=%d | loop=%s\n",
			summary.ActiveCooks,
			summary.OrdersDepth,
			summary.LoopState,
		)
	}
	report, err := linestatus.Read(runtimeDir)
	if err != nil {
		return err
	}
	for _, order := range report.Failed {
		reason := order.Reason
		if reason == "" {
			reason = "no reason recorded"
		}
		fmt.Fprintf(os.Stdout, "failed: %s | %s\n", order.ID, reason)
	}
	return nil
}

func readStatusSummary(runtimeDir string) (statusSummary, error) {
	active, loopState, err := readSessionSummary(runtimeDir)
	if err != nil {
		return statusSummary{}, err
	}
	ordersDepth, err := readOrdersDepth(filepath.Join(runtimeDir, "orders.json"))
	if err != nil {
		return statusSummary{}, err
	}
	return statusSummary{
		ActiveCooks: active,
		OrdersDepth: ordersDepth,
		LoopState:   loopState,
	}, nil
}

func readSessionSummary(runtimeDir string) (active int, loopState string, _ error) {
	status, err := statusfile.Read(filepath.Join(runtimeDir, "status.json"))
	if err != nil {
		return 0, "", err
	}
	active = len(status.Active)
	loopState = snapshot.NormalizeLoopState(status.LoopState)
	if loopState == "" {
		loopState = "running"
	}
	return active, loopState, nil
}

func pickLoopState(current, candidate string) string {
	current = snapshot.NormalizeLoopState(current)
	candidate = snapshot.NormalizeLoopState(candidate)
	if candidate == "" {
		return current
	}
	if loopStateRank(candidate) > loopStateRank(current) {
		return candidate
	}
	return current
}

func loopStateRank(state string) int {
	switch state {
	case "draining":
		return 3
	case "paused":
		return 2
	case "running":
		return 1
	default:
		return 0
	}
}

func readOrdersDepth(path string) (int, error) {
	orders, err := orderx.ReadOrders(path)
	if err != nil {
		return 0, err
	}
	return len(orders.Orders), nil
}
