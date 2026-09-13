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


def env_path() -> str:
    return os.environ.get("ATLAS_HUB_ENV") or os.path.join(
        os.environ.get("HERMES_HOME") or os.path.expanduser("~/.hermes"), ".env"
    )


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
DB = os.environ.get("ATLAS_HUB_DB") or os.path.join(os.path.dirname(env_path()), "state.db")
PORT = int(os.environ.get("ATLAS_HUB_PORT", "8643"))
PROFILE = os.environ.get("ATLAS_HUB_PROFILE", "default")
CACHE_TTL = float(os.environ.get("ATLAS_HUB_CACHE", "300"))

_cache = {
    "guilds": None,
    "guilds_at": 0.0,
    "channels": {},        # guild_id -> {channel_id: channel_obj}
    "channels_at": {},     # guild_id -> fetched_at
    "thread_parent": {},   # thread_id -> parent channel_id (or None)
}


def discord_get(path: str):
    req = urllib.request.Request(
        DISCORD_API + path,
        headers={"Authorization": f"Bot {TOKEN}", "User-Agent": "atlas-hub/0.1"},
    )
    with urllib.request.urlopen(req, timeout=15) as r:
        return json.load(r)


def get_guilds():
    now = time.time()
    if _cache["guilds"] is None or now - _cache["guilds_at"] > CACHE_TTL:
        _cache["guilds"] = discord_get("/users/@me/guilds")
        _cache["guilds_at"] = now
    return _cache["guilds"]


def channel_map(guild_id: str):
    now = time.time()
    if guild_id not in _cache["channels"] or now - _cache["channels_at"].get(guild_id, 0.0) > CACHE_TTL:
        chans = discord_get(f"/guilds/{guild_id}/channels")
        _cache["channels"][guild_id] = {c["id"]: c for c in chans}
        _cache["channels_at"][guild_id] = now
    return _cache["channels"][guild_id]


def resolve_thread_parent(thread_id: str):
    """Threads are not listed in guild channels; fetch once and cache."""
    if thread_id in _cache["thread_parent"]:
        return _cache["thread_parent"][thread_id]
    try:
        c = discord_get(f"/channels/{thread_id}")
        parent = c.get("parent_id")
    except Exception:
        parent = None
    _cache["thread_parent"][thread_id] = parent
    return parent


def resolve_location(chat_id: str, thread_id: str):
    """Return (guild_id, channel_obj, category_obj) for a session, or None."""
    for g in get_guilds():
        cmap = channel_map(g["id"])
        # direct channel session
        if chat_id and chat_id in cmap:
            chan = cmap[chat_id]
            cat = cmap.get(chan.get("parent_id") or "", None)
            return g["id"], chan, cat if (cat and cat.get("type") == 4) else None
        # thread session: thread_id -> parent channel
        if thread_id:
            parent = resolve_thread_parent(thread_id)
            if parent and parent in cmap:
                chan = cmap[parent]
                cat = cmap.get(chan.get("parent_id") or "", None)
                return g["id"], chan, cat if (cat and cat.get("type") == 4) else None
    return None


def load_sessions():
    con = sqlite3.connect(f"file:{DB}?mode=ro", uri=True)
    con.row_factory = sqlite3.Row
    try:
        return con.execute(
            "SELECT id, source, chat_id, chat_type, thread_id, display_name, title, "
            "last_activity_at, message_count, pinned "
            "FROM sessions WHERE hidden=0 AND archived=0 AND profile_name=? "
            "ORDER BY last_activity_at DESC",
            (PROFILE,),
        ).fetchall()
    finally:
        con.close()


def post_node(row):
    return {
        "kind": "post",
        "name": row["title"] or row["display_name"] or row["id"],
        "session_id": row["id"],
        "chat_type": row["chat_type"],
        "last_active": row["last_activity_at"] or 0,
        "message_count": row["message_count"] or 0,
        "pinned": bool(row["pinned"]),
    }


def build_tree():
    errors = []
    rows = load_sessions()

    discord_rows = [r for r in rows if r["source"] == "discord"]
    other_rows = [r for r in rows if r["source"] != "discord"]

    # ---- Discord side: guild -> category -> channel -> posts
    guilds_out = {}  # guild_id -> {name, categories: {name: {channels: {name: [posts]}}}}
    unfiled = []
    for r in discord_rows:
        try:
            loc = resolve_location(r["chat_id"], r["thread_id"])
        except Exception as e:
            errors.append(f"resolve {r['id']}: {e}")
            loc = None
        if not loc:
            unfiled.append(post_node(r))
            continue
        gid, chan, cat = loc
        gname = next((g["name"] for g in get_guilds() if g["id"] == gid), gid)
        g = guilds_out.setdefault(gid, {"name": gname, "cats": {}})
        ckey = cat["name"] if cat else "Unfiled"
        corder = cat.get("position", 99) if cat else 9999
        c = g["cats"].setdefault(ckey, {"order": corder, "chans": {}})
        ch = c["chans"].setdefault(chan["name"], {"order": chan.get("position", 99), "posts": []})
        ch["posts"].append(post_node(r))

    sections = []
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
            sections.append({"kind": "guild", "name": g["name"], "children": cat_nodes})

    # ---- Other sources: one category, one channel per source
    if other_rows:
        by_source = {}
        for r in other_rows:
            by_source.setdefault(r["source"], []).append(post_node(r))
        chan_nodes = [
            {"kind": "channel", "name": src, "children": sorted(posts, key=lambda p: -p["last_active"])}
            for src, posts in sorted(by_source.items())
        ]
        sections.append({"kind": "category", "name": "Other sessions", "children": chan_nodes})

    if unfiled:
        sections.append({
            "kind": "category",
            "name": "Unfiled",
            "children": [{"kind": "channel", "name": "unknown", "children": unfiled}],
        })

    return {"generated_at": time.time(), "sections": sections, "errors": errors}


class Handler(BaseHTTPRequestHandler):
    def do_GET(self):
        if self.path == "/health":
            self._json(200, {"ok": True, "service": "atlas-hub"})
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
    print(f"atlas-hub on 127.0.0.1:{PORT} (db={DB}, profile={PROFILE})", file=sys.stderr, flush=True)
    ThreadingHTTPServer(("127.0.0.1", PORT), Handler).serve_forever()
