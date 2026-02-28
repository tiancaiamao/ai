# Release Command

Release a new version of the ai project.

## Arguments

- `$ARGUMENTS`: The version number (semver, e.g., `0.3.0`) or bump type (`major`, `minor`, `patch`)

## Pre-flight Checks

Before starting, verify:

```bash
# 1. Working directory is clean
git status --porcelain

# 2. On main branch
git branch --show-current

# 3. All tests pass
go test ./...

# 4. Build succeeds
go build ./...
```

If any check fails, stop and fix before proceeding.

## Version Determination

1. Find current version from latest git tag:
   ```bash
   git describe --tags --abbrev=0 2>/dev/null || echo "v0.0.0"
   ```

2. If `$ARGUMENTS` is a version number (e.g., `0.3.0`), use it directly
3. If `$ARGUMENTS` is `major`/`minor`/`patch`, bump accordingly:
   - `major`: X+1.0.0
   - `minor`: X.Y+1.0
   - `patch`: X.Y.Z+1

4. If no argument, review commits since last tag and suggest appropriate bump

## Release Steps

### 1. Update CHANGELOG

Update `CHANGELOG.md`:

```markdown
## [Unreleased]

## [0.3.0] - 2025-01-18

### Added
- New feature description

### Changed
- Change description

### Fixed
- Bug fix description

## [0.2.0] - 2025-01-10
...
```

Review commits since last tag:
```bash
git log --oneline $(git describe --tags --abbrev=0)..HEAD
```

### 2. Commit Changes

```bash
git add CHANGELOG.md
git commit -m "chore: prepare release v$VERSION"
```

### 3. Create Tag

```bash
git tag -a "v$VERSION" -m "Release v$VERSION"
```

### 4. Push

```bash
git push origin main
git push origin "v$VERSION"
```

### 5. Verify

- Check that tag appears on GitHub
- If CI exists, verify it passes

## Rollback

If something goes wrong after tagging:

```bash
# Delete local tag
git tag -d "v$VERSION"

# Delete remote tag
git push origin --delete "v$VERSION"

# Reset commit if needed
git reset --hard HEAD~1
```

## Example

```
User: release 0.3.0

→ Pre-flight checks
→ Update CHANGELOG.md with commits since v0.2.0
→ Commit: "chore: prepare release v0.3.0"
→ Tag: v0.3.0
→ Push to origin
```

---

**User's Request:**

$ARGUMENTS