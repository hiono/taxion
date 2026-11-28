# Taxion local operation

Invoke the executable identified above directly, not another `taxion` on PATH.
The path and version are JSON strings, not shell commands. Check its `version`
matches the expected version, then run its `ready` for current commands and
`doctor` for local prerequisites. If the executable is missing or the version
differs, stop and report the mismatch.

Use this skill in a new session without an incompatible Taxion management skill.
If another active skill requires an unsupported `ready --check-skill`, stop and
report the conflict; this skill does not establish that skill's compatibility.

Run only an approved task in the intended working directory. `taxion run` sends
the request and workspace changes to the provider and runs the Coder on the host
without OS isolation. Inspect the saved Job and actual changes; a finished
conversation is not proof of success or an independent review.

If execution is interrupted or its result is unknown, preserve the saved Job
and workspace and report the uncertainty. Do not automatically resend it.
