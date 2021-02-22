package main

import (
	"context"
	"strings"

	"google.golang.org/grpc/metadata"
)

func getCustomer(ctx context.Context) string {
	// we'll just treat the "auth token" as if it is a
	// customer ID, but reject tokens that begin with "agent"
	// (those are auth tokens for support agents, not customers)
	cust := getAuthCode(ctx)
	if strings.HasPrefix(cust, "agent") {
		return ""
	}
	return cust
}

func getAgent(ctx context.Context) string {
	// we'll just treat the "auth token" as if it is an agent's
	// user ID, but reject tokens that don't begin with "agent"
	// (those are auth tokens for customers, not support agents)
	agent := getAuthCode(ctx)
	if !strings.HasPrefix(agent, "agent") {
		return ""
	}
	return agent
}

func getAuthCode(ctx context.Context) string {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return ""
	}
	vals := md.Get("authorization")
	if len(vals) != 1 {
		return ""
	}
	pieces := strings.SplitN(strings.ToLower(vals[0]), " ", 2)
	if len(pieces) != 2 {
		return ""
	}
	if pieces[0] != "token" {
		return ""
	}
	return pieces[1]
}
