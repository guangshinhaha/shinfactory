package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/guangshinhaha/shinfactory/cmdmeta"
	"github.com/guangshinhaha/shinfactory/loop"
	"github.com/spf13/cobra"
)

// controlSpec describes one control the web UI can send: its usage line and
// how positional args fill the command. The loop validates the values.
type controlSpec struct {
	name  string
	usage string // args after the name, for help
	min   int
	max   int // -1: the last arg takes the rest of the line
	fill  func(cmd *loop.ControlCommand, args []string)
}

func orderArg(cmd *loop.ControlCommand, args []string) { cmd.OrderID = args[0] }

func orderAndPrompt(cmd *loop.ControlCommand, args []string) {
	cmd.OrderID = args[0]
	cmd.Prompt = strings.Join(args[1:], " ")
}

var controlSpecs = []controlSpec{
	{name: "pause", fill: func(*loop.ControlCommand, []string) {}},
	{name: "resume", fill: func(*loop.ControlCommand, []string) {}},
	{name: "drain", fill: func(*loop.ControlCommand, []string) {}},
	{name: "stop-all", fill: func(*loop.ControlCommand, []string) {}},
	{name: "requeue", usage: "<order>", min: 1, max: 1, fill: orderArg},
	{name: "skip", usage: "<order>", min: 1, max: 1, fill: orderArg},
	{name: "advance", usage: "<order>", min: 1, max: 1, fill: orderArg},
	{name: "merge", usage: "<order>", min: 1, max: 1, fill: orderArg},
	{name: "reject", usage: "<order>", min: 1, max: 1, fill: orderArg},
	{name: "request-changes", usage: "<order> <what to change>", min: 2, max: -1, fill: orderAndPrompt},
	{name: "reorder", usage: "<order> [index]", min: 1, max: 2, fill: func(cmd *loop.ControlCommand, args []string) {
		cmd.OrderID = args[0]
		if len(args) > 1 {
			cmd.Value = args[1]
		}
	}},
	{name: "set-max-concurrency", usage: "<n>", min: 1, max: 1, fill: func(cmd *loop.ControlCommand, args []string) { cmd.Value = args[0] }},
	{name: "mode", usage: "<auto|supervised|manual>", min: 1, max: 1, fill: func(cmd *loop.ControlCommand, args []string) { cmd.Value = args[0] }},
	{name: "stop", usage: "<worktree or session>", min: 1, max: 1, fill: func(cmd *loop.ControlCommand, args []string) { cmd.Name = args[0] }},
	{name: "kill", usage: "<worktree or session>", min: 1, max: 1, fill: func(cmd *loop.ControlCommand, args []string) { cmd.Name = args[0] }},
	{name: "steer", usage: "<worktree or session> <message>", min: 2, max: -1, fill: func(cmd *loop.ControlCommand, args []string) {
		cmd.Target = args[0]
		cmd.Prompt = strings.Join(args[1:], " ")
	}},
	{name: "enqueue", usage: "<order> <prompt> [--provider --model --skill --task-key]", min: 2, max: -1, fill: orderAndPrompt},
	{name: "add-stage", usage: "<order> <task-key> [prompt] [--provider --model --skill]", min: 2, max: -1, fill: func(cmd *loop.ControlCommand, args []string) {
		cmd.OrderID = args[0]
		cmd.TaskKey = args[1]
		cmd.Prompt = strings.Join(args[2:], " ")
	}},
	{name: "park-review", usage: "<order> [reason]", min: 1, max: -1, fill: orderAndPrompt},
	{name: "edit-item", usage: "<order> [new title] [--provider --model --skill --task-key]", min: 1, max: -1, fill: orderAndPrompt},
}

func controlSpecByName(name string) (controlSpec, bool) {
	for _, spec := range controlSpecs {
		if spec.name == name {
			return spec, true
		}
	}
	return controlSpec{}, false
}

func controlNames() string {
	names := make([]string, len(controlSpecs))
	for i, spec := range controlSpecs {
		names[i] = spec.name
	}
	return strings.Join(names, ", ")
}

type controlFlags struct {
	provider, model, skill, taskKey string
	timeout                         time.Duration
	asJSON                          bool
}

func newControlCmd(app *App) *cobra.Command {
	var flags controlFlags
	cmd := &cobra.Command{
		Use:   "control <command> [args]",
		Short: cmdmeta.Short("control"),
		Long: "Send the same controls the web UI sends: " + controlNames() + ".\n" +
			"The command goes through the loop's control queue; this waits for the loop's ack.\n" +
			"Text that starts with - is read as a flag. Put -- before the arguments to send it as text:\n" +
			"  shinfactory control -- steer s1 \"-v was a typo\"",
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runControl(app, cmd.OutOrStdout(), args[0], args[1:], flags)
		},
	}
	cmd.Flags().StringVar(&flags.provider, "provider", "", "stage provider (enqueue, edit-item)")
	cmd.Flags().StringVar(&flags.model, "model", "", "stage model (enqueue, edit-item)")
	cmd.Flags().StringVar(&flags.skill, "skill", "", "stage skill (enqueue, edit-item)")
	cmd.Flags().StringVar(&flags.taskKey, "task-key", "", "stage task key (enqueue, edit-item)")
	cmd.Flags().DurationVar(&flags.timeout, "timeout", 15*time.Second, "how long to wait for the loop's ack; 0 sends without waiting")
	cmd.Flags().BoolVar(&flags.asJSON, "json", false, "print the ack as JSON")
	return cmd
}

func runControl(app *App, out io.Writer, name string, args []string, flags controlFlags) error {
	spec, ok := controlSpecByName(name)
	if !ok {
		return fmt.Errorf("unknown control %q (want one of: %s)", name, controlNames())
	}
	if len(args) < spec.min || (spec.max >= 0 && len(args) > spec.max) {
		return fmt.Errorf("control %s: wrong arguments; usage: %s", name, strings.TrimSpace(name+" "+spec.usage))
	}
	runtimeDir, err := app.RuntimeDir()
	if err != nil {
		return err
	}

	now := time.Now().UTC()
	cmd := loop.ControlCommand{
		ID:       fmt.Sprintf("cli-%d", now.UnixNano()),
		Action:   name,
		Provider: flags.provider,
		Model:    flags.model,
		Skill:    flags.skill,
		TaskKey:  flags.taskKey,
		At:       now,
	}
	if len(args) > 0 {
		spec.fill(&cmd, args)
	}
	if err := loop.AppendControlCommand(runtimeDir, cmd); err != nil {
		return err
	}
	if flags.timeout <= 0 {
		return printAck(out, flags.asJSON, loop.ControlAck{ID: cmd.ID, Action: name, Status: "queued", At: now})
	}

	ctx, cancel := context.WithTimeout(context.Background(), flags.timeout)
	defer cancel()
	ack, err := loop.WaitControlAck(ctx, runtimeDir, cmd.ID)
	if err != nil {
		return fmt.Errorf("%s was queued but the loop did not answer in %s; it is not running for %s, or is busy, and will apply the command when it reads the queue (%w)", name, flags.timeout, app.projectDir, err)
	}
	if err := printAck(out, flags.asJSON, ack); err != nil {
		return err
	}
	if ack.Status != "ok" {
		return errors.New(strings.TrimSpace(name + " failed: " + ack.Message))
	}
	return nil
}

func printAck(out io.Writer, asJSON bool, ack loop.ControlAck) error {
	if asJSON {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(ack)
	}
	line := fmt.Sprintf("%s: %s", ack.Action, ack.Status)
	if ack.Message != "" {
		line += " (" + ack.Message + ")"
	}
	_, err := fmt.Fprintln(out, line)
	return err
}
