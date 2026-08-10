---
title: "No secrets in logs"
agents: ["security"]
severity: blocker
priority: 20
tags: ["security", "secrets"]
---
Never log API keys, tokens, passwords, or other sensitive credentials.
Any code that passes these values to a logger (log.Printf, fmt.Fprintln
to stderr/stdout, etc.) must be flagged as a blocker.