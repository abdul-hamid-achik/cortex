/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

package main

import (
	"fmt"
	"strings"

	"github.com/abdul-hamid-achik/cortex/internal/domain"
	"github.com/abdul-hamid-achik/cortex/internal/kernel"
	"github.com/spf13/cobra"
)

var decisionCmd = &cobra.Command{
	Use:   "decision",
	Short: "Pause for, answer, or recover a bounded human decision",
}

var decisionRequestCmd = &cobra.Command{
	Use:   "request <taskId>",
	Short: "Pause a task and request one bounded human decision",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		k, err := kernelFor(cmd)
		if err != nil {
			return err
		}
		question, _ := cmd.Flags().GetString("question")
		requester, _ := cmd.Flags().GetString("requester")
		rawOptions, _ := cmd.Flags().GetStringArray("option")
		options, err := parseDecisionOptions(rawOptions)
		if err != nil {
			return err
		}
		env, err := k.RequestDecision(kernel.RequestDecisionInput{
			TaskID: args[0], Question: question, Options: options, Requester: requester,
		})
		if err != nil {
			return err
		}
		return emitEnvelope(cmd, env)
	},
}

var decisionAnswerCmd = &cobra.Command{
	Use:   "answer <taskId> <decisionId>",
	Short: "Record a selected option and resume the exact paused phase",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		k, err := kernelFor(cmd)
		if err != nil {
			return err
		}
		answer, _ := cmd.Flags().GetString("answer")
		responder, _ := cmd.Flags().GetString("responder")
		env, err := k.AnswerDecision(kernel.AnswerDecisionInput{
			TaskID: args[0], DecisionID: args[1], Answer: answer, Responder: responder,
		})
		if err != nil {
			return err
		}
		return emitEnvelope(cmd, env)
	},
}

var decisionResumeCmd = &cobra.Command{
	Use:   "resume <taskId>",
	Short: "Recover a task whose answered decision was not resumed after a crash",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		k, err := kernelFor(cmd)
		if err != nil {
			return err
		}
		env, err := k.ResumeDecision(args[0])
		if err != nil {
			return err
		}
		return emitEnvelope(cmd, env)
	},
}

func init() {
	decisionRequestCmd.Flags().String("question", "", "the bounded question a human must answer")
	decisionRequestCmd.Flags().StringArray("option", nil, "option as id=label|consequence (repeatable; at least two)")
	decisionRequestCmd.Flags().String("requester", "agent", "actor requesting the decision")
	_ = decisionRequestCmd.MarkFlagRequired("question")
	decisionAnswerCmd.Flags().String("answer", "", "selected option id")
	decisionAnswerCmd.Flags().String("responder", "human", "actor answering the decision")
	_ = decisionAnswerCmd.MarkFlagRequired("answer")
	decisionCmd.AddCommand(decisionRequestCmd, decisionAnswerCmd, decisionResumeCmd)
	rootCmd.AddCommand(decisionCmd)
}

func parseDecisionOptions(raw []string) ([]domain.DecisionOption, error) {
	options := make([]domain.DecisionOption, 0, len(raw))
	for _, value := range raw {
		id, rest, ok := strings.Cut(value, "=")
		if !ok || strings.TrimSpace(id) == "" || strings.TrimSpace(rest) == "" {
			return nil, fmt.Errorf("decision option %q must use id=label|consequence", value)
		}
		label, consequence, ok := strings.Cut(rest, "|")
		if !ok || strings.TrimSpace(label) == "" || strings.TrimSpace(consequence) == "" {
			return nil, fmt.Errorf("decision option %q must use id=label|consequence", value)
		}
		options = append(options, domain.DecisionOption{
			ID: strings.TrimSpace(id), Label: strings.TrimSpace(label), Consequence: strings.TrimSpace(consequence),
		})
	}
	return options, nil
}
