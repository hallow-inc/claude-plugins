# Plugin skill integrity

## ADDED Requirements

### Requirement: Skill frontmatter must parse so the skill can auto-trigger

Every skill's `SKILL.md` frontmatter SHALL be valid YAML that parses into a populated `name` and `description`. A skill whose frontmatter fails to parse loads with empty metadata and cannot be auto-triggered from its description, silently disabling it.

In particular, a plain (unquoted) YAML scalar SHALL NOT be used for a `description` value that contains a colon-space (`: `) sequence — YAML interprets `: ` as a mapping separator and the parse fails. Descriptions containing `: ` (e.g. "NOT for: …", "tag: …") SHALL use a folded block scalar (`description: >-`) or another form that parses the whole value as literal text without altering the wording.

#### Scenario: Validation reports no frontmatter errors

- **WHEN** `claude plugin validate` runs against the plugin
- **THEN** no skill reports a "YAML frontmatter failed to parse" error
- **AND** every skill's `name` and `description` load as non-empty

#### Scenario: A description contains a colon-space phrase

- **WHEN** a skill's description text includes a `: ` sequence such as "NOT for: X" or "tag: fargate"
- **THEN** the `description` field is written as a folded block scalar (or otherwise parse-safe form)
- **AND** the parsed description text matches the intended wording verbatim, with no clause dropped or reworded
