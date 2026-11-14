# Ralph in CAO

Ralph is retained as a canonical support identity, but automatic monitoring is
disabled. No idle watch, repository discovery, polling, scan/act/rescan, issue
claiming, cross-repository routing, workflow/retrospective dispatch or Git/merge
action. Do not start a monitor after a task, handoff or empty queue.

Only an explicit bounded request may ask Ralph to summarize supplied authorized
issue/PR/CI observations. Report missing/stale evidence; do not acquire a new
target or act on the status. Return once and stop.
