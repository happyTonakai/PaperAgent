#!/usr/bin/env python3
"""
Migrate old-format paper JSON files to new format.

Old format: paper content in top-level "content" field, no round-0 messages.
New format: paper content in messages[0] (round_number=0, role="user"),
            top-level content/initial_summary cleared on Save.

This script:
1. Backs up all paper files to ~/.config/paperagent/papers_backup_<timestamp>/
2. For each file missing round-0 user message with full content:
   - Creates it from top-level "content" field
3. For each file missing round-0 assistant message with summary:
   - Creates it from top-level "initial_summary" field
4. Writes back (without top-level content/initial_summary, matching SavePaper behavior)

Papers that already have round-0 messages are left untouched.
"""

import json
import os
import shutil
import sys
import time
from datetime import datetime

PAPERS_DIR = os.path.expanduser("~/.config/paperagent/papers")
BACKUP_DIR = os.path.expanduser(f"~/.config/paperagent/papers_backup_{datetime.now().strftime('%Y%m%d_%H%M%S')}")


def migrate():
    if not os.path.isdir(PAPERS_DIR):
        print(f"❌ Papers directory not found: {PAPERS_DIR}")
        sys.exit(1)

    # Step 1: Backup
    print(f"📦 Backing up to {BACKUP_DIR} ...")
    shutil.copytree(PAPERS_DIR, BACKUP_DIR)
    print("✅ Backup done")

    migrated = 0
    skipped = 0
    lost = 0

    for fname in sorted(os.listdir(PAPERS_DIR)):
        if not fname.endswith(".json"):
            continue

        fpath = os.path.join(PAPERS_DIR, fname)
        with open(fpath, "r") as f:
            data = json.load(f)

        msgs = data.get("messages", [])

        # Check if already in new format (has round-0 user with content matching top-level)
        has_r0u = any(
            m.get("round_number") == 0 and m.get("role") == "user"
            for m in msgs
        )
        has_r0a = any(
            m.get("round_number") == 0 and m.get("role") == "assistant"
            for m in msgs
        )

        top_content = data.get("content", "") or ""
        top_summary = data.get("initial_summary", "") or ""

        needs_migration = False

        # Check if top-level content exists but is not in messages
        if top_content and not has_r0u:
            needs_migration = True
        elif top_content and has_r0u:
            # R0 user exists, but is it the full content or something else?
            r0u_content = ""
            for m in msgs:
                if m.get("round_number") == 0 and m.get("role") == "user":
                    r0u_content = m.get("content", "")
                    break
            if len(r0u_content) < len(top_content) * 0.5:  # R0 user is truncated digest, not full content
                needs_migration = True

        # Check if top-level summary exists but is not in messages
        if top_summary and not has_r0a:
            needs_migration = True
        elif top_summary and has_r0a:
            r0a_content = ""
            for m in msgs:
                if m.get("round_number") == 0 and m.get("role") == "assistant":
                    r0a_content = m.get("content", "")
                    break
            if r0a_content != top_summary:
                needs_migration = True

        if not needs_migration:
            # Check if content is completely lost
            has_content = bool(top_content) or has_r0u
            if not has_content and len(msgs) > 0:
                print(f"  ⚠️  LOST (no content, no r0 user): {fname}")
                lost += 1
            else:
                print(f"  ✓ OK (already migrated): {fname}")
                skipped += 1
            continue

        # Perform migration
        print(f"  🔧 Migrating: {fname}", end="")
        title = data.get("title", fname[:8])
        if title:
            print(f" ({title[:60]})", end="")
        print()

        # Build new messages list
        new_msgs = []
        r0_user_exists = False
        r0_assistant_exists = False

        for m in msgs:
            if m.get("round_number") == 0 and m.get("role") == "user":
                r0_user_exists = True
                # Only replace if current content is not the full paper
                if top_content and len(m.get("content", "")) < len(top_content) * 0.5:
                    m["content"] = top_content
                    m["token_count"] = len(top_content) // 4
            if m.get("round_number") == 0 and m.get("role") == "assistant":
                r0_assistant_exists = True
                if top_summary and m.get("content", "") != top_summary and top_summary not in m.get("content", ""):
                    m["content"] = top_summary
                    m["token_count"] = len(top_summary) // 4
            new_msgs.append(m)

        # Prepend round-0 user if missing
        if top_content and not r0_user_exists:
            new_msgs.insert(0, {
                "round_number": 0,
                "role": "user",
                "content": top_content,
                "token_count": len(top_content) // 4,
                "created_at": "0001-01-01T00:00:00Z"
            })

        # Prepend round-0 assistant if missing
        if top_summary and not r0_assistant_exists:
            # Find index after r0 user
            insert_at = 0
            for i, m in enumerate(new_msgs):
                if m.get("round_number") == 0 and m.get("role") == "user":
                    insert_at = i + 1
                    break
            new_msgs.insert(insert_at, {
                "round_number": 0,
                "role": "assistant",
                "content": top_summary,
                "token_count": len(top_summary) // 4,
                "prompt_tokens": 0,
                "completion_tokens": 0,
                "cached_tokens": 0,
                "skip_context": True,
                "created_at": "0001-01-01T00:00:00Z"
            })

        data["messages"] = new_msgs

        # Clear top-level fields (matching SavePaper behavior)
        data["content"] = ""
        data["initial_summary"] = ""

        with open(fpath, "w") as f:
            json.dump(data, f, indent=2, ensure_ascii=False)

        migrated += 1

    print()
    print(f"📊 Summary:")
    print(f"   Migrated:  {migrated}")
    print(f"   Skipped:   {skipped}")
    print(f"   Lost:      {lost}")
    print(f"   Total:     {migrated + skipped + lost}")
    print()
    if lost > 0:
        print(f"⚠️  {lost} paper(s) have permanently lost content (no top-level content and no r0 user).")
    print(f"📦 Backup at: {BACKUP_DIR}")


if __name__ == "__main__":
    migrate()
