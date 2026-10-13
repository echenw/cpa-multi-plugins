// scheduler_pick.go implements the pluginabi.MethodSchedulerPick capability
// for the trae plugin.
//
// issue #2: the host unconditionally probes scheduler.pick on every plugin.
// A missing method made the host log
//
//	scheduler rejected auth pick plugin_id=trae error=unknown method:
//	scheduler.pick
//
// and fail the whole /v1/chat/completions request with HTTP 500 — enabling
// the trae plugin alone was enough to break every request, even ones routed
// to other plugins.
//
// Trae deliberately does NOT take over auth routing: each request goes to
// whichever account the host routed it to (see intl_management.go). So the
// handler validates the request envelope and defers every pick back to the
// built-in scheduler (Handled: false). The method now exists — the host
// stops treating the plugin as broken — and routing behavior is unchanged.
package main

import (
	"encoding/json"
	"strings"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

func handleSchedulerPick(raw []byte) ([]byte, error) {
	var req pluginapi.SchedulerPickRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, err
	}
	// T19: session mode is strictly opt-in. Every other mode (including the
	// default off) keeps the historical no-routing philosophy — Handled:
	// false tells the host to run its built-in scheduler for this pick.
	// Only the retry path (executor rotation) and session mode intervene.
	if loadedSchedulerMode() != schedulerModeSession {
		return okEnvelope(pluginapi.SchedulerPickResponse{Handled: false})
	}
	// scheduler_mode=session: per-conversation sticky routing over trae
	// candidates. Non-trae candidates stay host-routed; an empty/unusable
	// set defers (Handled: false) so cross-provider failover still works.
	var cands []traeSessionCandidate
	for _, c := range req.Candidates {
		if c.Provider != providerName {
			continue
		}
		if strings.TrimSpace(c.ID) == "" {
			continue
		}
		uid := sessionCandidateUID(c)
		cands = append(cands, traeSessionCandidate{
			ID:        strings.TrimSpace(c.ID),
			UID:       uid,
			Disabled:  sessionCandidateDisabled(c),
			Exhausted: sessionPoolUnusable(uid),
		})
	}
	picked := pickSessionAuth(extractSessionKey(req), cands)
	if picked == "" {
		return okEnvelope(pluginapi.SchedulerPickResponse{Handled: false})
	}
	return okEnvelope(pluginapi.SchedulerPickResponse{AuthID: picked, Handled: true})
}
