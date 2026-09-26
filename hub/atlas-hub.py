#!/usr/bin/env python3
"""atlas-hub — thin local service that assembles Atlas's workstream tree.

Reads the Hermes state.db (read-only) for session<->Discord bindings and the
Discord REST API (bot token) for server structure (categories/channels), then
serves one JSON tree mirroring the Discord shape:

    GET /health          -> liveness (no auth)
    GET /tree            -> the assembled tree (Bearer $API_SERVER_KEY)

Config via env (defaults match head):
    ATLAS_HUB_ENV      .env holding DISCORD_BOT_TOKEN / API_SERVER_KEY
                       (default: $HERMES_HOME/.env or ~/.hermes/.env)
    ATLAS_HUB_DB       state.db path (default: alongside the .env)
    ATLAS_HUB_PORT     listen port (default 8643, binds 127.0.0.1)
    ATLAS_HUB_PROFILE  sessions.profile_name filter (default: "default")
    ATLAS_HUB_CACHE    channel cache TTL seconds (default 300)

Stdlib only — run with any python3.
"""

import json
import os
import sqlite3
import sys
import time
import urllib.request
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

DISCORD_API = "https://discord.com/api/v10"


def hermes_home() -> str:
    return os.environ.get("HERMES_HOME") or os.path.expanduser("~/.hermes")


def env_path(profile_home: str = "") -> str:
    if profile_home:
        return os.path.join(profile_home, ".env")
    return os.environ.get("ATLAS_HUB_ENV") or os.path.join(hermes_home(), ".env")


def read_env_file(path: str) -> dict:
    out = {}
    try:
        with open(path) as f:
            for line in f:
                line = line.strip()
                if "=" in line and not line.startswith("#"):
                    k, v = line.split("=", 1)
                    out[k.strip()] = v.strip()
    except OSError:
        pass
    return out


ENV = read_env_file(env_path())
TOKEN = os.environ.get("DISCORD_BOT_TOKEN") or ENV.get("DISCORD_BOT_TOKEN", "")
AUTH = os.environ.get("API_SERVER_KEY") or ENV.get("API_SERVER_KEY", "")
PORT = int(os.environ.get("ATLAS_HUB_PORT", "8643"))
CACHE_TTL = float(os.environ.get("ATLAS_HUB_CACHE", "300"))

# Friendlier channel names for non-discord session sources in the tree.
SOURCE_LABELS = {
    "cli": "CLI",
    "api_server": "API",
    "cron": "Cron",
    "telegram": "Telegram",
    "matrix": "Matrix",
    "webhook": "Webhook",
}


def discover_profiles():
    """Profile registry: the hermes home (default) + every profiles/<name>/.

    Each entry carries its own state.db and its own Discord bot token (read
    from that profile's .env). Profiles without a state.db are skipped.
    ATLAS_HUB_PROFILES=default,debbie optionally narrows the registry.
    """
    home = hermes_home()
    reg = [{"name": "default", "home": home, "display": "Nolan"}]
    pdir = os.path.join(home, "profiles")
    if os.path.isdir(pdir):
        for name in sorted(os.listdir(pdir)):
            d = os.path.join(pdir, name)
            if name.startswith(".") or not os.path.isdir(d):
                continue
            reg.append({"name": name, "home": d, "display": name.capitalize()})
    out = []
    for p in reg:
        penv = read_env_file(os.path.join(p["home"], ".env"))
        p["token"] = penv.get("DISCORD_BOT_TOKEN") or (TOKEN if p["name"] == "default" else "")
        p["db"] = (os.environ.get("ATLAS_HUB_DB") if p["name"] == "default" else "") or os.path.join(p["home"], "state.db")
        p["stats"] = os.path.join(p["home"], "stats")
        if os.path.exists(p["db"]):
            out.append(p)
    filt = [s.strip() for s in os.environ.get("ATLAS_HUB_PROFILES", "").split(",") if s.strip()]
    if filt:
        out = [p for p in out if p["name"] in filt]
    return out


PROFILES = discover_profiles()

_cache = {
    "guilds": {},          # profile -> guilds
    "guilds_at": {},
    "channels": {},        # (profile, guild_id) -> {channel_id: channel_obj}
    "channels_at": {},
    "thread_parent": {},   # (profile, thread_id) -> parent channel_id (or None)
}


def discord_get(path: str, token: str):
    req = urllib.request.Request(
        DISCORD_API + path,
        headers={"Authorization": f"Bot {token}", "User-Agent": "atlas-hub/0.1"},
    )
    with urllib.request.urlopen(req, timeout=15) as r:
        return json.load(r)


def get_guilds(prof):
    key = prof["name"]
    now = time.time()
    if key not in _cache["guilds"] or now - _cache["guilds_at"].get(key, 0.0) > CACHE_TTL:
        _cache["guilds"][key] = discord_get("/users/@me/guilds", prof["token"])
        _cache["guilds_at"][key] = now
    return _cache["guilds"][key]


def channel_map(guild_id: str, prof):
    key = (prof["name"], guild_id)
    now = time.time()
    if key not in _cache["channels"] or now - _cache["channels_at"].get(key, 0.0) > CACHE_TTL:
        chans = discord_get(f"/guilds/{guild_id}/channels", prof["token"])
        _cache["channels"][key] = {c["id"]: c for c in chans}
        _cache["channels_at"][key] = now
    return _cache["channels"][key]


def resolve_thread_parent(thread_id: str, prof):
    """Threads are not listed in guild channels; fetch once and cache."""
    key = (prof["name"], thread_id)
    if key in _cache["thread_parent"]:
        return _cache["thread_parent"][key]
    try:
        c = discord_get(f"/channels/{thread_id}", prof["token"])
        parent = c.get("parent_id")
    except Exception:
        parent = None
    _cache["thread_parent"][key] = parent
    return parent


def resolve_location(chat_id: str, thread_id: str, prof):
    """Return (guild_id, channel_obj, category_obj) for a session, or None."""
    for g in get_guilds(prof):
        cmap = channel_map(g["id"], prof)
        # direct channel session
        if chat_id and chat_id in cmap:
            chan = cmap[chat_id]
            cat = cmap.get(chan.get("parent_id") or "", None)
            return g["id"], chan, cat if (cat and cat.get("type") == 4) else None
        # thread session: thread_id -> parent channel
        if thread_id:
            parent = resolve_thread_parent(thread_id, prof)
            if parent and parent in cmap:
                chan = cmap[parent]
                cat = cmap.get(chan.get("parent_id") or "", None)
                return g["id"], chan, cat if (cat and cat.get("type") == 4) else None
    return None


def find_session(session_id: str):
    """(profile, row) for a session id across every profile db, else (None, None)."""
    for prof in PROFILES:
        con = sqlite3.connect(f"file:{prof['db']}?mode=ro", uri=True)
        con.row_factory = sqlite3.Row
        try:
            row = con.execute(
                "SELECT source, thread_id, chat_id FROM sessions WHERE id=?", (session_id,)
            ).fetchone()
        finally:
            con.close()
        if row is not None:
            return prof, row
    return None, None


def load_sessions(prof):
    con = sqlite3.connect(f"file:{prof['db']}?mode=ro", uri=True)
    con.row_factory = sqlite3.Row
    try:
        # Each db belongs to one profile; the default db additionally carries
        # legacy NULL-profile rows (pre-multiprofile sessions of the same profile).
        return con.execute(
            "SELECT id, source, chat_id, chat_type, thread_id, display_name, title, "
            "last_activity_at, message_count, pinned "
            "FROM sessions WHERE hidden=0 AND archived=0 AND (profile_name=? OR profile_name IS NULL) "
            "ORDER BY last_activity_at DESC",
            (prof["name"],),
        ).fetchall()
    finally:
        con.close()


def post_node(row, prof):
    return {
        "kind": "post",
        "name": row["title"] or row["display_name"] or row["id"],
        "session_id": row["id"],
        "profile": prof["name"],
        "chat_type": row["chat_type"],
        "last_active": row["last_activity_at"] or 0,
        "message_count": row["message_count"] or 0,
        "pinned": bool(row["pinned"]),
    }


def build_tree():
    errors = []
    sections = []
    for prof in PROFILES:
        try:
            node = build_profile_section(prof, errors)
        except Exception as e:
            errors.append(f"profile {prof['name']}: {e}")
            continue
        if node is not None:
            sections.append(node)
    return {"generated_at": time.time(), "sections": sections, "errors": errors}


def build_profile_section(prof, errors):
    """One profile's subtree: discord guilds + other sources, or None when empty."""
    rows = load_sessions(prof)
    discord_rows = [r for r in rows if r["source"] == "discord"]
    other_rows = [r for r in rows if r["source"] != "discord"]

    children = []

    # ---- Discord side: guild -> category -> channel -> posts
    guilds_out = {}  # guild_id -> {name, cats: {name: {chans: {name: [posts]}}}}
    unfiled = []
    for r in discord_rows:
        try:
            loc = resolve_location(r["chat_id"], r["thread_id"], prof)
        except Exception as e:
            errors.append(f"resolve {r['id']}: {e}")
            loc = None
        if not loc:
            unfiled.append(post_node(r, prof))
            continue
        gid, chan, cat = loc
        gname = next((g["name"] for g in get_guilds(prof) if g["id"] == gid), gid)
        g = guilds_out.setdefault(gid, {"name": gname, "cats": {}})
        ckey = cat["name"] if cat else "Unfiled"
        corder = cat.get("position", 99) if cat else 9999
        c = g["cats"].setdefault(ckey, {"order": corder, "chans": {}})
        ch = c["chans"].setdefault(chan["name"], {"order": chan.get("position", 99), "posts": []})
        ch["posts"].append(post_node(r, prof))

    for gid, g in sorted(guilds_out.items()):
        cat_nodes = []
        for cname in sorted(g["cats"], key=lambda k: (g["cats"][k]["order"], k)):
            c = g["cats"][cname]
            chan_nodes = []
            for chname in sorted(c["chans"], key=lambda k: (c["chans"][k]["order"], k)):
                posts = sorted(c["chans"][chname]["posts"], key=lambda p: -p["last_active"])
                if posts:
                    chan_nodes.append({"kind": "channel", "name": chname, "children": posts})
            if chan_nodes:
                cat_nodes.append({"kind": "category", "name": cname, "children": chan_nodes})
        if cat_nodes:
            children.append({"kind": "guild", "name": g["name"], "children": cat_nodes})

    # ---- Other sources: one category, one channel per source
    if other_rows:
        by_source = {}
        for r in other_rows:
            by_source.setdefault(r["source"], []).append(post_node(r, prof))
        chan_nodes = [
            {"kind": "channel", "name": SOURCE_LABELS.get(src, src), "children": sorted(posts, key=lambda p: -p["last_active"])}
            for src, posts in sorted(by_source.items())
        ]
        children.append({"kind": "category", "name": "Other sessions", "children": chan_nodes})

    if unfiled:
        children.append({
            "kind": "category",
            "name": "Unfiled",
            "children": [{"kind": "channel", "name": "unknown", "children": unfiled}],
        })

    if not children:
        return None
    return {"kind": "profile", "name": prof["display"], "profile": prof["name"], "children": children}


# ---- mirror: relay atlas-originated messages into discord threads ----

def _post_discord_message(channel_id: str, content: str, token: str):
    payload = json.dumps({"content": content}).encode()
    req = urllib.request.Request(
        f"{DISCORD_API}/channels/{channel_id}/messages",
        data=payload,
        method="POST",
        headers={
            "Authorization": f"Bot {token}",
            "Content-Type": "application/json",
            "User-Agent": "atlas-hub/0.1",
        },
    )
    with urllib.request.urlopen(req, timeout=20) as r:
        return json.load(r)


def _chunks(text: str, limit: int = 1900):
    out, cur = [], ""
    for line in text.split("\n"):
        if len(line) > limit:
            if cur:
                out.append(cur)
                cur = ""
            for i in range(0, len(line), limit):
                out.append(line[i:i + limit])
            continue
        if cur and len(cur) + len(line) + 1 > limit:
            out.append(cur)
            cur = ""
        cur = cur + ("\n" if cur else "") + line
    if cur:
        out.append(cur)
    return out or [""]


def mirror_message(session_id: str, role: str, content: str):
    """Post one message into the session's Discord thread (if it has one).

    The posts are made by the session's own profile bot; each gateway
    hard-drops its own bot's messages on ingest, so mirroring can never
    re-trigger a turn.
    """
    prof, row = find_session(session_id)
    if prof is None or row is None:
        return {"ok": False, "error": "unknown session"}
    if row["source"] != "discord" or not row["thread_id"]:
        return {"ok": False, "error": "no discord thread for session"}
    target = row["thread_id"]
    if role == "user":
        quoted = "\n".join("> " + ln for ln in (content or "").split("\n"))
        text = f"-# ⌨️ atlas — Overtoneblue\n{quoted}"
    else:
        text = content or ""
    ids = []
    for chunk in _chunks(text):
        if not chunk.strip():
            continue
        resp = _post_discord_message(target, chunk, prof["token"])
        ids.append(resp.get("id"))
    return {"ok": True, "chunks": len(ids), "message_ids": ids}


# ---- native-parity turn mirror: reasoning, tool lines, reply, stats card ----

_TOOL_EMOJIS = {
    "terminal": "💻", "close_terminal": "🖥️", "read_terminal": "🖥️",
    "patch": "🔧", "read_file": "📖", "write_file": "✍️", "search_files": "🔎",
    "discord": "⚙️", "discord_admin": "⚙️", "process_manage": "⚙️",
    "delegate_task": "🔀", "memory": "🧠", "web_search": "🌐", "web_extract": "🌐",
    "browser_navigate": "🌐", "vision_analyze": "🖼️", "todo_list": "📋",
    "cronjob_manage": "⏰", "skill_manage": "📖", "skill_view": "📖",
    "skills_list": "📖", "clarify": "❓", "code_execution": "🐍",
}


def _tool_call_line(name: str, arguments: str) -> str:
    """Approximate the gateway's Discord tool line for one tool call."""
    emoji = _TOOL_EMOJIS.get(name, "⚙️")
    head = f"{emoji} {name}"
    try:
        args = json.loads(arguments or "{}")
    except Exception:
        args = {}
    if not isinstance(args, dict) or not args:
        return head + "..."
    if set(args.keys()) == {"command"} and isinstance(args.get("command"), str):
        cmd = args["command"]
        if len(cmd) > 500:
            cmd = cmd[:497] + "..."
        return f"{head}\n```\n{cmd}\n```"
    keys = list(args.keys())
    args_str = json.dumps(args, ensure_ascii=False, default=str)
    if len(args_str) > 40:
        args_str = args_str[:37] + "..."
    return f"{head}({keys})\n{args_str}"


def _reason_block(reasoning: str) -> str:
    """Render reasoning in the gateway's Discord subtext style."""
    r = (reasoning or "").strip()
    if not r:
        return ""
    lines = "\n".join("-# " + ln if ln.strip() else "-#" for ln in r.split("\n"))
    return f"-# 💭 Reasoning\n{lines}\n\n"


def _render_card(snap: dict) -> str:
    """Mirror of hooks/stats-card/handler.py render()."""
    parts = []
    tps = snap.get("last_call_gen_tok_s") or snap.get("last_call_eff_tok_s")
    if tps is not None:
        parts.append(f"⚡ {tps:g} tok/s")
    ttfb = snap.get("last_call_ttfb_s")
    if ttfb is not None:
        parts.append(f"ttfb {ttfb:g}s")
    dur = snap.get("turn_duration_s")
    if dur is not None:
        parts.append(f"{dur:g}s")

    def fmt(n):
        try:
            return f"{int(n):,}"
        except Exception:
            return "0"

    parts.append(f"{fmt(snap.get('out_tokens'))} out / {fmt(snap.get('in_tokens'))} in")
    cache = snap.get("cache_hit_pct")
    if cache:
        parts.append(f"{cache}% cache")
    calls = int(snap.get("calls") or 1)
    if calls > 1:
        parts.append(f"{calls} calls")
        avg_in = snap.get("avg_in_per_call")
        if avg_in is not None:
            parts.append(f"avg {fmt(round(avg_in))} in/call")
    cost = snap.get("cost_usd")
    if cost is not None:
        parts.append(f"${cost:.4f}")
    return " · ".join(parts)


def _read_card_state(prof):
    try:
        p = os.path.join(prof["stats"], "card_state.json")
        with open(p) as fh:
            return json.load(fh)
    except Exception:
        return None


def _post_chunked(channel_id: str, text: str, token: str) -> list:
    ids = []
    chunks = _chunks(text)
    n = len(chunks)
    for i, chunk in enumerate(chunks, 1):
        if not chunk.strip():
            continue
        body = chunk if n == 1 else f"{chunk} ({i}/{n})"
        resp = _post_discord_message(channel_id, body, token)
        ids.append(resp.get("id"))
    return ids


def _mirror_turn_once(session_id: str, since: float, dry: bool = False):
    """Relay a completed turn natively: tool lines, reasoning+reply, stats card."""
    prof, sess = find_session(session_id)
    if prof is None or sess is None:
        return {"ok": False, "error": "unknown session"}
    if sess["source"] != "discord" or not sess["thread_id"]:
        return {"ok": False, "error": "no discord thread for session"}
    con = sqlite3.connect(f"file:{prof['db']}?mode=ro", uri=True)
    con.row_factory = sqlite3.Row
    try:
        rows = con.execute(
            "SELECT id, role, content, tool_calls, tool_name, reasoning, timestamp "
            "FROM messages WHERE session_id=? AND timestamp >= ? ORDER BY id",
            (session_id, since - 2),
        ).fetchall()
    finally:
        con.close()

    # Turn boundary: the last user echo at/after `since`; everything after it.
    user_rows = [r for r in rows if r["role"] == "user"]
    if not user_rows:
        return {"ok": False, "error": "no user message found after `since`"}
    turn_start_id = user_rows[-1]["id"]
    turn = [r for r in rows if r["id"] > turn_start_id]

    assistants = [r for r in turn if r["role"] == "assistant"]
    if not assistants:
        return {"ok": False, "error": "no assistant messages yet"}
    last_asst_id = assistants[-1]["id"]

    posts = []
    for r in assistants:
        content = r["content"] or ""
        try:
            tool_calls = json.loads(r["tool_calls"]) if r["tool_calls"] else []
        except Exception:
            tool_calls = []
        if r["id"] == last_asst_id:
            text = _reason_block(r["reasoning"]) + content
            if text.strip():
                posts.append(text)
        else:
            if content.strip():
                posts.append(content)
        for tc in tool_calls or []:
            fn = (tc.get("function") or {}) if isinstance(tc, dict) else {}
            line = _tool_call_line(str(fn.get("name") or "?"), str(fn.get("arguments") or ""))
            if line:
                posts.append(line)

    # Stats card: read fresh card_state for THIS session (brief wait — the
    # recorder writes it at turn end).
    snap = None
    card_deadline = time.time() + 2.5
    while True:
        cand = _read_card_state(prof)
        if isinstance(cand, dict) and cand.get("session_id") == session_id:
            try:
                if float(cand.get("ts") or 0) >= since - 10:
                    snap = cand
                    break
            except Exception:
                pass
        if time.time() >= card_deadline:
            break
        time.sleep(0.3)
    if snap is not None:
        card = _render_card(snap)
        if card:
            posts.append(card)

    if dry:
        return {"ok": True, "dry": True, "posts": len(posts), "preview": [p[:300] for p in posts]}
    target = sess["thread_id"]
    ids = []
    for post in posts:
        ids.extend(_post_chunked(target, post, prof["token"]))
    return {"ok": True, "posts": len(posts), "chunks": len(ids), "message_ids": ids}


def mirror_turn(session_id: str, since: float, dry: bool = False):
    """Relay a completed turn; retries briefly while the turn's writes land."""
    deadline = time.time() + 4.0
    while True:
        res = _mirror_turn_once(session_id, since, dry)
        pending = res.get("error") in {
            "no assistant messages yet",
            "no user message found after `since`",
        }
        if res.get("ok") or not pending or time.time() >= deadline:
            return res
        time.sleep(0.5)


def _turn_cards(session_id: str):
    """Group metrics.jsonl api_call records into per-turn cards."""
    prof, _ = find_session(session_id)
    if prof is None:
        return {"ok": True, "turns": [], "total": {"turns": 0, "calls": 0, "cost_usd": None}}
    path = os.path.join(prof["stats"], "metrics.jsonl")
    turns = {}
    try:
        with open(path) as fh:
            for line in fh:
                if session_id not in line:
                    continue
                try:
                    r = json.loads(line)
                except Exception:
                    continue
                if r.get("session_id") != session_id or r.get("event") != "api_call":
                    continue
                tid = str(r.get("turn_id") or f"ts:{r.get('ts')}")
                t = turns.get(tid)
                if t is None:
                    t = turns[tid] = {
                        "calls": 0, "out": 0, "in": 0, "cache_read": 0, "dur": 0.0,
                        "cost": 0.0, "last_eff": None, "last_gen": None, "last_ttfb": None,
                        "end": 0.0, "model": None, "provider": None,
                    }
                t["calls"] += 1
                t["out"] += int(r.get("out_tokens") or 0)
                t["in"] += int(r.get("in_tokens") or 0)
                t["cache_read"] += int(r.get("cache_read") or 0)
                t["dur"] += float(r.get("duration_s") or 0)
                if r.get("cost_usd") is not None:
                    t["cost"] += float(r["cost_usd"])
                t["last_eff"] = r.get("eff_tok_s")
                t["last_gen"] = r.get("gen_tok_s")
                t["last_ttfb"] = r.get("ttfb_s")
                t["end"] = max(t["end"], float(r.get("ts") or 0))
                t["model"] = r.get("model") or t["model"]
                t["provider"] = r.get("provider") or t["provider"]
    except OSError:
        return {"ok": True, "turns": [], "total": {"turns": 0, "calls": 0, "cost_usd": None}}

    cards = []
    total = {"turns": 0, "calls": 0, "cost_usd": 0.0, "out_tokens": 0, "in_tokens": 0}
    for tid, t in sorted(turns.items(), key=lambda kv: kv[1]["end"]):
        cache_pct = round(100 * t["cache_read"] / t["in"]) if t["in"] else None
        snap = {
            "v": 1, "session_id": session_id, "turn_id": tid, "ts": t["end"],
            "model": t["model"], "provider": t["provider"],
            "calls": t["calls"], "out_tokens": t["out"], "in_tokens": t["in"],
            "cache_hit_pct": cache_pct,
            "turn_duration_s": round(t["dur"], 2),
            "last_call_eff_tok_s": t["last_eff"],
            "last_call_ttfb_s": t["last_ttfb"],
            "last_call_gen_tok_s": t["last_gen"],
            "cost_usd": round(t["cost"], 6) if t["cost"] else None,
            "avg_in_per_call": round(t["in"] / t["calls"], 1) if t["calls"] else None,
        }
        cards.append({"end_ts": t["end"], "line": _render_card(snap), "snap": snap})
        total["turns"] += 1
        total["calls"] += t["calls"]
        total["cost_usd"] = (total["cost_usd"] or 0) + t["cost"]
        total["out_tokens"] += t["out"]
        total["in_tokens"] += t["in"]
    total["cost_usd"] = round(total["cost_usd"], 6) if total["cost_usd"] else None
    return {"ok": True, "turns": cards, "total": total}


def _fts_query(q: str) -> str:
    import re

    toks = [t for t in re.split(r"\s+", q.strip()) if t]
    if not toks:
        return ""
    quoted = ['"' + t.replace('"', '""') + '"' for t in toks]
    quoted[-1] += "*"
    return " ".join(quoted)


def search_messages(q: str, limit: int = 40):
    """Full-text search across every profile's message store (FTS5, LIKE fallback)."""
    fts = _fts_query(q)
    if not fts:
        return {"ok": True, "results": []}
    results = []
    for prof in PROFILES:
        con = sqlite3.connect(f"file:{prof['db']}?mode=ro", uri=True)
        con.row_factory = sqlite3.Row
        try:
            try:
                rows = con.execute(
                    "SELECT m.id AS message_id, m.session_id, m.role, m.timestamp, "
                    "snippet(messages_fts, 0, '', '', '…', 14) AS snip, s.title "
                    "FROM messages_fts JOIN messages m ON m.id = messages_fts.rowid "
                    "LEFT JOIN sessions s ON s.id = m.session_id "
                    "WHERE messages_fts MATCH ? ORDER BY rank LIMIT ?",
                    (fts, int(limit)),
                ).fetchall()
            except sqlite3.OperationalError:
                like = f"%{q.strip()}%"
                rows = con.execute(
                    "SELECT id AS message_id, session_id, role, timestamp, "
                    "substr(content, 1, 140) AS snip, (SELECT title FROM sessions s WHERE s.id = messages.session_id) AS title "
                    "FROM messages WHERE content LIKE ? ORDER BY id DESC LIMIT ?",
                    (like, int(limit)),
                ).fetchall()
        except Exception:
            rows = []
        finally:
            con.close()
        for r in rows:
            results.append({
                "message_id": r["message_id"],
                "session_id": r["session_id"],
                "role": r["role"],
                "timestamp": r["timestamp"],
                "snippet": r["snip"],
                "title": r["title"],
                "profile": prof["name"],
            })
    # Rank is per-db and not comparable across profiles; merge by recency.
    results.sort(key=lambda r: -(r.get("timestamp") or 0))
    return {"ok": True, "results": results[: int(limit)]}


class Handler(BaseHTTPRequestHandler):
    def do_GET(self):
        if self.path.split("?")[0] == "/stats/card":
            if AUTH and self.headers.get("Authorization") != f"Bearer {AUTH}":
                self._json(401, {"error": "unauthorized"})
                return
            try:
                from urllib.parse import parse_qs, urlparse

                q = parse_qs(urlparse(self.path).query)
                sid = (q.get("session_id") or [""])[0]
                since = float((q.get("since") or ["0"])[0] or 0)
                prof, _ = find_session(sid)
                if prof is None:
                    self._json(200, {"ok": False, "error": "unknown session"})
                    return
                deadline = time.time() + 4.0
                while True:
                    snap = _read_card_state(prof)
                    if isinstance(snap, dict) and snap.get("session_id") == sid:
                        try:
                            if float(snap.get("ts") or 0) >= since - 10:
                                self._json(200, {"ok": True, "card": _render_card(snap), "snap": snap})
                                return
                        except Exception:
                            pass
                    if time.time() >= deadline:
                        self._json(200, {"ok": False, "error": "no fresh card"})
                        return
                    time.sleep(0.4)
            except Exception as e:
                self._json(500, {"error": str(e)})
            return
        if self.path == "/health":
            self._json(200, {"ok": True, "service": "atlas-hub",
                             "profiles": [p["name"] for p in PROFILES]})
            return
        if self.path.split("?")[0] == "/stats/turns":
            if AUTH and self.headers.get("Authorization") != f"Bearer {AUTH}":
                self._json(401, {"error": "unauthorized"})
                return
            try:
                from urllib.parse import parse_qs, urlparse

                q = parse_qs(urlparse(self.path).query)
                sid = (q.get("session_id") or [""])[0]
                self._json(200, _turn_cards(sid))
            except Exception as e:
                self._json(500, {"error": str(e)})
            return
        if self.path.split("?")[0] == "/search":
            if AUTH and self.headers.get("Authorization") != f"Bearer {AUTH}":
                self._json(401, {"error": "unauthorized"})
                return
            try:
                from urllib.parse import parse_qs, urlparse

                q = parse_qs(urlparse(self.path).query)
                query = (q.get("q") or [""])[0]
                limit = int((q.get("limit") or ["40"])[0] or 40)
                self._json(200, search_messages(query, limit))
            except Exception as e:
                self._json(500, {"error": str(e)})
            return
        if self.path.split("?")[0] == "/tree":
            if AUTH and self.headers.get("Authorization") != f"Bearer {AUTH}":
                self._json(401, {"error": "unauthorized"})
                return
            try:
                self._json(200, build_tree())
            except Exception as e:
                self._json(500, {"error": str(e)})
            return
        self._json(404, {"error": "not found"})

    def do_POST(self):
        path = self.path.split("?")[0]
        if path in ("/mirror", "/mirror_turn"):
            if AUTH and self.headers.get("Authorization") != f"Bearer {AUTH}":
                self._json(401, {"error": "unauthorized"})
                return
            try:
                n = int(self.headers.get("Content-Length") or 0)
                body = json.loads(self.rfile.read(n) or b"{}")
                if path == "/mirror":
                    res = mirror_message(
                        str(body.get("session_id") or ""),
                        str(body.get("role") or ""),
                        str(body.get("content") or ""),
                    )
                else:
                    res = mirror_turn(
                        str(body.get("session_id") or ""),
                        float(body.get("since") or 0),
                        bool(body.get("dry") or False),
                    )
                self._json(200, res)
            except Exception as e:
                self._json(500, {"error": str(e)})
            return
        self._json(404, {"error": "not found"})

    def _json(self, code: int, obj):
        body = json.dumps(obj).encode()
        self.send_response(code)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def log_message(self, fmt, *args):
        sys.stderr.write("atlas-hub: " + fmt % args + "\n")


if __name__ == "__main__":
    if not TOKEN:
        print("atlas-hub: WARNING: no DISCORD_BOT_TOKEN found", file=sys.stderr)
    plist = ", ".join(f"{p['name']}({p['display']})" for p in PROFILES) or "none"
    print(f"atlas-hub on 127.0.0.1:{PORT} — profiles: {plist}", file=sys.stderr, flush=True)
    ThreadingHTTPServer(("127.0.0.1", PORT), Handler).serve_forever()
