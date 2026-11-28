---
name: taxion-local
description: Operate Taxion's local collaboration CLI for an approved coding task.
---

# Taxion local operation

Use the Taxion binary that installed this skill. Check `taxion version`, then
run `taxion ready` for the current commands and `taxion doctor` for local
prerequisites. If another Taxion version is on PATH, use the intended binary's
absolute path instead.

Run only an approved task in the intended working directory. `taxion run` sends
the request and workspace changes to the provider and runs the Coder on the host
without OS isolation. Inspect the saved Job and actual changes; a finished
conversation is not proof of success or an independent review.

If execution is interrupted or its result is unknown, preserve the saved Job
and workspace and report the uncertainty. Do not automatically resend it.
