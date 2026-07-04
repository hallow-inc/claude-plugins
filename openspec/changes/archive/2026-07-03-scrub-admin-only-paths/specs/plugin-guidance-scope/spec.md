# Plugin guidance scope

## ADDED Requirements

### Requirement: Non-admin audience — no admin-only recipes in user flows

The `hallow-windmill` plugin's audience is exclusively non-admin Hallow engineers. Its skills and docs SHALL NOT route a user into performing a Windmill action that row-level security reserves for workspace admins. Admin-only how-to content SHALL be removed rather than dual-pathed (no "if you are an admin, do X" branches); admin procedures live in the platform repo.

Admin-gated actions, for this requirement, are: writing/publishing to `f/shared/`; creating a group; seeding variables under `f/platform_secrets/`; pushing a trigger or schedule with an elevated `permissioned_as: u/sandbox`; and running `set-permissioned-as` or `workspace connect-slack`.

Deploying one's own work is NOT an admin-gated action: pushing an entity into a folder where the user is a Writer is ordinary and requires no group. The plugin SHALL NOT frame normal deploys as gated by a privileged tier, and SHALL NOT reference `g/wm_deployers` (a deleted group). The role noun "deployer," meaning whoever performed a push, remains valid where it describes server stamping of `email` / `permissioned_as`.

Read/call-side usage of shared resources (recommending, composing, or calling `f/shared/` atoms; referencing `f/platform_secrets/` variables by name from a script) is permitted for non-admins and is out of scope of this restriction.

#### Scenario: User asks to make a tool runnable workspace-wide

- **WHEN** a non-admin asks that "anyone at Hallow" be able to run their tool
- **THEN** the plugin routes the tool to a team folder (`f/<team>/`) as the ceiling of non-admin capability
- **AND** it does not present `f/shared/` as a selectable destination
- **AND** it does not present a recipe for publishing to `f/shared/`

#### Scenario: User asks how to publish a shared atom

- **WHEN** a non-admin asks to publish a reusable shared atom
- **THEN** the plugin does not surface a step-by-step `f/shared/` publish recipe
- **AND** no skill routing table entry directs the user into such a recipe

#### Scenario: A script needs elevated privileges

- **WHEN** guidance covers a script that needs to run with elevated identity
- **THEN** it states that elevation is admin-only and the non-admin provides the script for an admin to wrap and push
- **AND** it does not instruct the non-admin to push a `permissioned_as: u/sandbox` trigger themselves

#### Scenario: Reference material explains an admin-gated failure

- **WHEN** troubleshooting or reference content explains why an admin-gated action fails (e.g. a 403, a `permissioned_as` mismatch, a "Trigger not found" folder-ACL rejection)
- **THEN** that explanatory content is retained as reference
- **AND** it is not phrased as a step instructing the non-admin to perform the admin-gated action

#### Scenario: Guidance describes deploy vs. elevated run-as

- **WHEN** a skill or doc describes deploying an entity or the `set-permissioned-as` capability
- **THEN** deploying into a folder where the user is a Writer is described as ordinary, requiring no privileged group
- **AND** `set-permissioned-as` and elevated `permissioned_as` pushes are gated to "admin" only
- **AND** no reference cites `g/wm_deployers` as a permission tier
- **AND** the term "deployer" is retained only where it means the identity that performed a push
