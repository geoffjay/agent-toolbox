---
title: "Require error wrapping"
agents: ["static_analysis"]
severity: major
priority: 10
tags: ["go", "errors"]
---
All error returns must be wrapped with fmt.Errorf using %w to preserve
the error chain. Bare error returns without wrapping should be flagged
as a major finding.