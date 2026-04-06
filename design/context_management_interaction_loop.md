# Context Management Interaction Loop

This document describes the interaction loop for the context management system.

## Overview

The context management interaction loop handles how the agent manages its context window, including when to trigger compaction, truncation, and other context operations.

## Validation Note

**2026-04-06**: Validated that the context management refactor integrates correctly with the prompt builder — `TestBuildSystemPrompt` passes and the system prompt includes expected context management instructions.