# Jujutsu (JJ) Usage Guidelines and Pitfalls

## Overview

This document provides essential guidelines for using Jujutsu (JJ) version control system safely and effectively. JJ is a powerful VCS that uses a different model from Git, so understanding its concepts and pitfalls is crucial.

## Core Concepts

### 1. Working Copy vs Commits
- **Working Copy**: The current state of your files (represented by `@`)
- **Commits**: Immutable snapshots of your code
- Unlike Git, JJ automatically creates commits for your working copy changes

### 2. Change IDs vs Commit IDs
- **Change ID**: Stable identifier that persists across rewrites (e.g., `nsuvqtro`)
- **Commit ID**: Hash that changes when commit is modified (e.g., `09707ab3`)
- Always use Change IDs when referencing commits in commands

### 3. Branches
- JJ branches are just pointers to commits
- Multiple branches can point to the same commit
- Branch names are not as central as in Git

## Safe Usage Guidelines

### 1. Always Check Status First
```bash
# Check current state before any operation
jj status

# Get a visual overview
jj log --oneline -n 10
```

### 2. Describe Your Changes
```bash
# Set description for current working copy
jj describe -m "Add background caching feature for English prayers"

# Or use editor
jj describe
```

### 2. Safe Commit Creation
```bash
# First describe your working copy changes
jj describe -m "Your commit message"

# Then create new commit with current changes
jj commit -m "Your commit message"

# Or commit with detailed description
jj commit -m "Add background caching system

- Automatic English prayers caching after page load
- Batch processing with 50 prayers per batch
- Smart duplicate detection
- Comprehensive test coverage"
```

### 4. Bookmark Management
```bash
# Create new bookmark
jj bookmark create feature-branch

# Set bookmark to current commit
jj bookmark set feature-branch

# Set bookmark to specific commit
jj bookmark set feature-branch -r <change-id>

# List bookmarks
jj bookmark list
```

### 5. Safe Pushing
```bash
# Push all bookmarks (default behavior)
jj git push

# Push specific bookmark
jj git push --bookmark main

# Check what will be pushed first
jj log -n 5
```

## Common Pitfalls and How to Avoid Them

### ❌ Pitfall 1: Forgetting to Describe Changes
**Problem**: Working copy commits have no description
**Solution**: Always use `jj describe` before committing

```bash
# Bad: Creates commit with no description
jj commit

# Good: Always describe first
jj describe -m "Fix background caching bug"
jj commit
```

### ❌ Pitfall 2: Using Commit IDs Instead of Change IDs
**Problem**: Commit IDs change when you rewrite history
**Solution**: Use Change IDs (the short alphanumeric string)

```bash
# Bad: Using commit hash
jj rebase -r 09707ab3

# Good: Using change ID
jj rebase -r nsuvqtro
```

### ❌ Pitfall 3: Not Checking Status Before Operations
**Problem**: Operating on wrong commits or branches
**Solution**: Always check status first

```bash
# Always do this first
jj status
jj log --oneline -n 5

# Then proceed with operations
jj commit -m "Your changes"
```

### ❌ Pitfall 4: Pushing Without Verification
**Problem**: Pushing unwanted changes or wrong branches
**Solution**: Review what you're pushing

```bash
# Check what will be pushed first
jj log -n 10

# Good: Push specific bookmark only
jj git push --bookmark main
```

### ❌ Pitfall 5: Confusion with Working Copy
**Problem**: Not understanding that working copy is automatically tracked
**Solution**: Remember @ is your working copy

```bash
# Working copy is always @
jj status  # Shows working copy changes

# Commit working copy changes
jj commit -m "Save current work"
```

## Essential Commands Cheat Sheet

### Status and History
```bash
jj status                    # Current working copy status
jj log                       # Full history
jj log --oneline -n 10      # Compact history (last 10)
jj show                      # Show current commit details
jj show <change-id>          # Show specific commit
```

### Making Changes
```bash
jj describe -m "message"     # Describe current working copy
jj commit -m "message"       # Commit working copy changes
jj split                     # Split current changes into multiple commits
jj squash                    # Squash changes into parent
```

### Bookmark Operations
```bash
jj bookmark create <name>    # Create new bookmark
jj bookmark set <name>       # Set bookmark to current commit
jj bookmark list             # List all bookmarks
jj bookmark delete <name>    # Delete bookmark
```

### Navigation
```bash
jj checkout <change-id>      # Switch to different commit
jj checkout main             # Switch to main branch
jj new                       # Create new empty commit
jj new <change-id>           # Create new commit on top of specified commit
```

### Rewriting History
```bash
jj rebase -r <change-id>     # Rebase specific commit
jj rebase -s <change-id>     # Rebase commit and descendants
jj abandon <change-id>       # Abandon commit (children become orphans)
```

### Git Interop
```bash
jj git push --bookmark <name> # Push specific bookmark
jj git push                   # Push all bookmarks
jj git fetch                  # Fetch from remote
jj git import                 # Import Git refs
```

## Best Practices for Our Project

### 1. Feature Development Workflow
```bash
# 1. Check current state
jj status

# 2. Create feature bookmark
jj bookmark create feature/background-caching

# 3. Make changes and describe them
jj describe -m "Add background caching for English prayers"

# 4. Commit when ready
jj commit -m "Implement background caching system"

# 5. Push to remote
jj git push --bookmark feature/background-caching
```

### 2. Testing Before Push
```bash
# Always test before pushing
npm test
npm run lint

# Then describe and commit
jj describe -m "Add background caching feature with tests"
jj commit

# Push only after tests pass
jj git push --bookmark main
```

### 3. Reviewing Changes
```bash
# Review what changed
jj show

# Review specific files
jj show --name-only
jj show app.js

# Compare with previous version
jj diff -r @-
```

## Emergency Recovery

### If You Make a Mistake
```bash
# Undo last operation (if possible)
jj op undo

# Restore file from previous commit
jj restore --from @- <file>

# Abandon problematic commit
jj abandon <change-id>
```

### If You're Lost
```bash
# Check operation history
jj op log

# Get detailed status
jj status
jj log --oneline -n 20

# Ask for help
jj help <command>
```

## Our Project Specific Guidelines

### 1. Commit Message Format
```
Add background caching feature for English prayers

- Automatic caching after page load
- Batch processing with API rate limiting
- Comprehensive test coverage
- Updated documentation
```

### 2. Bookmark Naming
- `feature/description` for new features
- `fix/description` for bug fixes
- `docs/description` for documentation
- `test/description` for test improvements

### 3. Testing Requirements
- All tests must pass before committing
- Run `npm test` and `npm run lint`
- Add tests for new features
- Update documentation

### 4. Push Strategy
- Always push to feature bookmarks first
- Use `--bookmark` flag to push specific bookmarks
- Never push directly to main without review

## Common JJ vs Git Differences

| Operation | Git | JJ |
|-----------|-----|-----|
| Current changes | `git status` | `jj status` |
| Commit changes | `git commit` | `jj commit` |
| View history | `git log` | `jj log` |
| Switch commits | `git checkout` | `jj checkout` |
| Create branch | `git branch` | `jj bookmark create` |
| Push changes | `git push` | `jj git push` |

## Troubleshooting Common Issues

### Issue: Features Stop Working After Updates
**Symptoms**: Previously working functionality suddenly breaks
**Likely Cause**: Conflicting external patches or fixes
**Solution**: 
1. Check for `fixes.json` or similar patch files in parent directories
2. Look for Go programs that apply patches (e.g., `apply_fixes.go`)
3. Remove outdated patches that conflict with current implementation
4. Test thoroughly after removing patches

### Issue: Repository Scope Confusion
**Symptoms**: Some files can't be committed with JJ
**Likely Cause**: Files are outside the JJ repository scope
**Solution**:
1. Use `jj status` to see which files are tracked
2. Consider moving all project files into the JJ repository
3. Handle external files separately with appropriate version control

### Issue: Search or Core Features Break
**Symptoms**: Main application features fail unexpectedly
**Investigation Steps**:
1. Check for multiple implementations of the same function
2. Look for external patch systems overriding your code
3. Verify that new features don't conflict with existing architecture
4. Run full test suite to identify integration issues

### Issue: Background Processes Interfere
**Symptoms**: New background features cause UI problems
**Prevention**:
1. Add proper error handling in background processes
2. Use appropriate delays to avoid race conditions
3. Test background features with existing functionality
4. Monitor for memory or performance impacts

## Remember

1. **Always check status first**: `jj status`
2. **Describe your changes**: `jj describe -m "..."`
3. **Test before committing**: `npm test && npm run lint`
4. **Use Change IDs, not Commit IDs**: The short alphanumeric string
5. **Push specific bookmarks**: `jj git push --bookmark <name>`
6. **Review before pushing**: `jj show` and `jj log`
7. **Check for external patches**: Look for `fixes.json` or similar files
8. **Test integrations**: Verify new features work with existing code

---

*Keep this document handy and refer to it often until JJ workflows become second nature!*