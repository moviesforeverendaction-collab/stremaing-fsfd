"""
TG-FileStreamBot — Python UI Bot (Kurigram)
Spawned automatically by the Go binary on startup.
Handles: /start UI, colored buttons, force-subscribe (fsub).
Go bot handles: file streaming at /stream/:id
"""

import asyncio
import logging
import os
import sys
import json
import signal

from kurigram import Client, filters
from kurigram.enums import ButtonStyle
from kurigram.types import (
    InlineKeyboardMarkup,
    InlineKeyboardButton,
    Message,
    CallbackQuery,
    ChatMember,
)
from kurigram.errors import (
    UserNotParticipant,
    ChannelInvalid,
    ChatAdminRequired,
    PeerIdInvalid,
)

# ── Config from environment (passed by Go via os.environ) ────────────────────
API_ID        = int(os.environ["API_ID"])
API_HASH      = os.environ["API_HASH"]
BOT_TOKEN     = os.environ["BOT_TOKEN"]
STREAM_HOST   = os.environ.get("HOST", "http://localhost:8080")
SUPPORT_LINK  = os.environ.get("SUPPORT_LINK", "https://t.me/YourSupport")
ABOUT_LINK    = os.environ.get("ABOUT_LINK", "https://github.com/EverythingSuckz/TG-FileStreamBot")
DEVELOPER_LINK= os.environ.get("DEVELOPER_LINK", "https://t.me/YourUsername")
ADMIN_IDS     = [int(x) for x in os.environ.get("ADMIN_IDS", "").split(",") if x.strip()]

# ── State (in-memory; swap for SQLite/Redis in production) ───────────────────
fsub_channel      = None   # int chat_id
fsub_channel_link = None   # str public/invite link

logging.basicConfig(
    level=logging.INFO,
    format="%(asctime)s [PYBOT] %(levelname)s %(message)s",
    datefmt="%d/%m/%Y %I:%M:%S %p",
    stream=sys.stdout,
)
log = logging.getLogger("pybot")

app = Client(
    "fsb_python",
    api_id=API_ID,
    api_hash=API_HASH,
    bot_token=BOT_TOKEN,
    workdir=os.environ.get("PYBOT_WORKDIR", "."),
)

# ── Helpers ───────────────────────────────────────────────────────────────────

def btn(text, *, cb=None, url=None, style=ButtonStyle.DEFAULT):
    if url:
        return InlineKeyboardButton(text=text, url=url, style=style)
    return InlineKeyboardButton(text=text, callback_data=cb, style=style)


async def is_subscribed(client: Client, user_id: int) -> bool:
    if fsub_channel is None:
        return True
    try:
        member: ChatMember = await client.get_chat_member(fsub_channel, user_id)
        return member.status.value not in ("left", "kicked", "banned")
    except UserNotParticipant:
        return False
    except Exception as e:
        log.warning("fsub check error: %s", e)
        return True  # fail-open on unexpected errors


def fsub_markup() -> InlineKeyboardMarkup:
    rows = []
    if fsub_channel_link:
        rows.append([btn("📢 Join Channel", url=fsub_channel_link,
                         style=ButtonStyle.PRIMARY)])
    rows.append([btn("✅ I've Joined — check me", cb="check_sub",
                     style=ButtonStyle.SUCCESS)])
    return InlineKeyboardMarkup(rows)


def start_markup() -> InlineKeyboardMarkup:
    return InlineKeyboardMarkup([
        [
            btn("📣 Support",    url=SUPPORT_LINK,   style=ButtonStyle.DANGER),
            btn("ℹ️ About",      url=ABOUT_LINK,     style=ButtonStyle.PRIMARY),
        ],
        [
            btn("👨‍💻 Developer", url=DEVELOPER_LINK, style=ButtonStyle.SUCCESS),
        ],
    ])


def file_markup(stream_link: str, download_link: str, has_stream: bool) -> InlineKeyboardMarkup:
    row = [btn("⬇️ Download", url=download_link, style=ButtonStyle.PRIMARY)]
    if has_stream:
        row.append(btn("▶️ Stream", url=stream_link, style=ButtonStyle.SUCCESS))
    return InlineKeyboardMarkup([row])

# ── /start ────────────────────────────────────────────────────────────────────

@app.on_message(filters.command("start") & filters.private)
async def cmd_start(client: Client, message: Message):
    user = message.from_user
    first = user.first_name or "there"

    if not await is_subscribed(client, user.id):
        await message.reply(
            "👋 **Hello!**\n\n"
            "You need to **join our channel** before you can use this bot.\n"
            "Subscribe and then press the button below.",
            reply_markup=fsub_markup(),
        )
        return

    await message.reply(
        f"👋 **Hello, {first}!**\n\n"
        "I convert any Telegram file into a **stream link** "
        "or **download link** instantly.\n\n"
        "📤 Just send me any **video**, **audio**, or **document** to get started.\n\n"
        "──────────────────\n"
        "🎬 **Stream** — watch directly in your browser\n"
        "⬇️ **Download** — get a direct download link",
        reply_markup=start_markup(),
    )

# ── Callback: I've Joined ─────────────────────────────────────────────────────

@app.on_callback_query(filters.regex("^check_sub$"))
async def cb_check_sub(client: Client, query: CallbackQuery):
    user = query.from_user
    if await is_subscribed(client, user.id):
        await query.message.delete()
        await query.message.reply(
            f"✅ **Verified! Welcome, {user.first_name}!**\n\n"
            "Now send me any file to get your stream/download link.",
            reply_markup=start_markup(),
        )
    else:
        await query.answer(
            "❌ You haven't joined yet. Please join and try again.",
            show_alert=True,
        )

# ── fsub management ───────────────────────────────────────────────────────────

def admin_only(func):
    async def wrapper(client, message):
        if message.from_user.id not in ADMIN_IDS:
            await message.reply("❌ You are not authorised to use this command.")
            return
        await func(client, message)
    wrapper.__name__ = func.__name__
    return wrapper


@app.on_message(filters.command("setfsub") & filters.private)
@admin_only
async def cmd_set_fsub(client: Client, message: Message):
    global fsub_channel, fsub_channel_link

    parts = message.text.strip().split(maxsplit=1)
    if len(parts) < 2:
        await message.reply(
            "**Usage:** `/setfsub @channel` or `/setfsub -100xxxxxxxxxx`\n\n"
            "The bot must be an admin of that channel."
        )
        return

    target = parts[1].strip()
    try:
        target = int(target)
    except ValueError:
        pass

    try:
        chat = await client.get_chat(target)
        fsub_channel = chat.id
        if chat.username:
            fsub_channel_link = f"https://t.me/{chat.username}"
        else:
            try:
                fsub_channel_link = await client.export_chat_invite_link(chat.id)
            except Exception:
                fsub_channel_link = None

        await message.reply(
            f"✅ **Force-subscribe enabled!**\n\n"
            f"**Channel:** {chat.title}\n"
            f"**ID:** `{chat.id}`\n"
            f"**Link:** {fsub_channel_link or 'private — no public link'}",
        )
        log.info("fsub channel set → %s (%s)", chat.title, chat.id)

    except (ChannelInvalid, PeerIdInvalid):
        await message.reply("❌ Channel not found. Make sure the bot is an admin there.")
    except ChatAdminRequired:
        await message.reply("❌ Bot needs admin rights in that channel.")
    except Exception as e:
        await message.reply(f"❌ Error: `{e}`")


@app.on_message(filters.command("removefsub") & filters.private)
@admin_only
async def cmd_remove_fsub(client: Client, message: Message):
    global fsub_channel, fsub_channel_link
    fsub_channel = None
    fsub_channel_link = None
    await message.reply("✅ Force-subscribe has been **disabled**.")
    log.info("fsub disabled")


@app.on_message(filters.command("fsub") & filters.private)
@admin_only
async def cmd_fsub_status(client: Client, message: Message):
    if fsub_channel is None:
        await message.reply("ℹ️ Force-subscribe is currently **disabled**.")
        return
    try:
        chat = await client.get_chat(fsub_channel)
        await message.reply(
            f"📢 **Force-subscribe is active**\n\n"
            f"**Channel:** {chat.title}\n"
            f"**ID:** `{chat.id}`\n"
            f"**Link:** {fsub_channel_link or 'private'}",
        )
    except Exception as e:
        await message.reply(f"⚠️ fsub set to `{fsub_channel}` but error: `{e}`")

# ── File handler — fsub gate for media messages ───────────────────────────────
# The Go bot (same token, gotgproto dispatcher) handles the actual forwarding
# and link generation. This Python handler ONLY enforces the fsub gate.
# If the user passes fsub, we do nothing and let Go handle the message.

@app.on_message(
    filters.private
    & (filters.document | filters.video | filters.audio
       | filters.photo | filters.voice | filters.video_note)
)
async def handle_file(client: Client, message: Message):
    user = message.from_user
    if not await is_subscribed(client, user.id):
        await message.reply(
            "🔒 **Access Restricted**\n\n"
            "Please join our channel first.",
            reply_markup=fsub_markup(),
        )
        # Return without processing — Go bot won't see it either since
        # gotgproto and kurigram share the same update stream on the same token.
        # To avoid double-processing, run this Python bot on a DIFFERENT token
        # (a second bot) that just does the UI, OR use the approach below where
        # Python ONLY blocks non-subscribers and lets Go handle the rest.
        # With a single token, comment this handler out and handle fsub in Go
        # using the Go fsub middleware added in internal/bot/middleware.go.
        return

# ── Entry ─────────────────────────────────────────────────────────────────────

def handle_sigterm(sig, frame):
    log.info("Received signal %s, shutting down Python bot...", sig)
    app.stop()
    sys.exit(0)

if __name__ == "__main__":
    signal.signal(signal.SIGTERM, handle_sigterm)
    signal.signal(signal.SIGINT, handle_sigterm)
    log.info("Python UI bot starting...")
    app.run()
