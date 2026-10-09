# Triage Labels

The skills speak in terms of five canonical triage roles. This file maps those roles to the actual label strings used in this repo's issue tracker.

| Label in mattpocock/skills | Label in our tracker | Meaning                                  |
| -------------------------- | -------------------- | ---------------------------------------- |
| `needs-triage`             | `needs-triage`       | Maintainer needs to evaluate this issue  |
| `needs-info`               | `needs-info`         | Waiting on reporter for more information |
| `ready-for-agent`          | `ready-for-agent`    | Fully specified, ready for an AFK agent  |
| `ready-for-human`          | `ready-for-human`    | Requires human implementation            |
| `wontfix`                  | `wontfix`            | Will not be actioned                     |

When a skill mentions a role (e.g. "apply the AFK-ready triage label"), use the corresponding label string from this table.

Edit the "Label in our tracker" column to match whatever vocabulary you actually use.

## Extra labels

| Label  | Meaning                                                        |
| ------ | -------------------------------------------------------------- |
| `spec` | The issue is a spec (for example, one published by `/to-spec`) |

Every issue a skill publishes as a spec gets the `spec` label **and no triage label**. This overrides any skill instruction to apply `ready-for-agent` (or another triage label) to a spec.

A spec is the parent of the tickets `/to-tickets` creates from it; the tickets carry the triage labels, never the spec itself. When triaging, skip issues labelled `spec`.
