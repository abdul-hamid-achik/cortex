/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

package main

import (
	"github.com/abdul-hamid-achik/cortex/internal/kernel"
	"github.com/spf13/cobra"
)

var beginChangeCmd = &cobra.Command{
	Use:   "begin-change <taskId>",
	Short: "Claim bounded change ownership and enter the changing phase",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		k, err := kernelFor(cmd)
		if err != nil {
			return err
		}
		actor, _ := cmd.Flags().GetString("actor")
		ttl, _ := cmd.Flags().GetDuration("ttl")
		env, err := k.BeginChange(kernel.BeginChangeInput{TaskID: args[0], Actor: actor, TTL: ttl})
		if err != nil {
			return err
		}
		return emitEnvelope(cmd, env)
	},
}

var leaseCmd = &cobra.Command{
	Use:   "lease",
	Short: "Renew or release bounded change ownership",
}

var leaseRenewCmd = &cobra.Command{
	Use:   "renew <taskId>",
	Short: "Renew an active change lease owned by the same actor",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		k, err := kernelFor(cmd)
		if err != nil {
			return err
		}
		actor, _ := cmd.Flags().GetString("actor")
		ttl, _ := cmd.Flags().GetDuration("ttl")
		env, err := k.RenewChangeLease(kernel.ChangeLeaseInput{TaskID: args[0], Actor: actor, TTL: ttl})
		if err != nil {
			return err
		}
		return emitEnvelope(cmd, env)
	},
}

var leaseReleaseCmd = &cobra.Command{
	Use:   "release <taskId>",
	Short: "Release a change lease owned by the actor",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		k, err := kernelFor(cmd)
		if err != nil {
			return err
		}
		actor, _ := cmd.Flags().GetString("actor")
		env, err := k.ReleaseChangeLease(kernel.ReleaseChangeLeaseInput{TaskID: args[0], Actor: actor})
		if err != nil {
			return err
		}
		return emitEnvelope(cmd, env)
	},
}

func init() {
	beginChangeCmd.Flags().String("actor", "", "stable agent/person taking change ownership")
	beginChangeCmd.Flags().Duration("ttl", kernel.DefaultChangeLeaseTTL, "bounded ownership duration (1s to 1h)")
	_ = beginChangeCmd.MarkFlagRequired("actor")
	leaseRenewCmd.Flags().String("actor", "", "current lease owner")
	leaseRenewCmd.Flags().Duration("ttl", kernel.DefaultChangeLeaseTTL, "renewal duration (1s to 1h)")
	_ = leaseRenewCmd.MarkFlagRequired("actor")
	leaseReleaseCmd.Flags().String("actor", "", "current lease owner")
	_ = leaseReleaseCmd.MarkFlagRequired("actor")
	leaseCmd.AddCommand(leaseRenewCmd, leaseReleaseCmd)
	rootCmd.AddCommand(beginChangeCmd, leaseCmd)
}
