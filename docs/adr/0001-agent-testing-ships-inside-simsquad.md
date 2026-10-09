# Agent testing ships inside simsquad as skills copied out of the binary

The test-run procedure (previously the separate simsquad-pilot repo, run by
starting an agent session inside a clone of it) moves into simsquad. The
binary embeds the skill files and `simsquad skill install` writes them into
the app repository, where they are committed. We chose this over a separate
repo because that forced users to leave their project to run a test and let
the procedure drift from the installed CLI (dry run 001 nearly misdiagnosed
a missing `bundle_id` this way), and over a Claude Code plugin because a
plugin versions with its marketplace, not with the binary it drives. The
binary itself still never calls an LLM: skill files are inert text it
copies out, and skill compatibility is checked by a contract number rather
than an exact version match.
