"""
TG-FileStreamBot — Python UI Bot (Kurigram)
Auto-spawned by the Go binary. Handles /start UI, colored buttons, fsub.
"""

# ── PATH BOOTSTRAP — must be first, before any kurigram import ───────────────
# Insert the site-packages dir Go installed kurigram into.
# This works even if PYTHONPATH env var is not picked up for any reason.
import sys
import os

_site = os.environ.get("PYBOT_SITE", "")
if _site and _site not in sys.path:
    sys.path.insert(0, _site)

# ── Stdlib imports ────────────────────────────────────────────────────────────
import logging
import signal

# ── Kurigram imports (now resolvable) ────────────────────────────────────────
from kurigram import Client, filters
from kurigram.enums import ButtonStyle
from kurigram.errors import (
    ChannelInvalid,
    ChatAdminRequired,
    PeerIdInvalid,
    UserNotParticipant,
)
from kurigram.types import (
    CallbackQuery,
    ChatMember,
    InlineKeyboardButton,
    InlineKeyboardMarkup,
    Message,
)

# ── Config (injected by Go via environment) ───────────────────────────────────
API_ID         = int(os.environ["API_ID"])
API_HASH       = os.environ["API_HASH"]
BOT_TOKEN      = os.environ["BOT_TOKEN"]
SUPPORT_LINK   = os.environ.get("SUPPORT_LINK", "https://t.me/YourSupport")
ABOUT_LINK     = os.environ.get("ABOUT_LINK", "https://github.com/EverythingSuckz/TG-FileStreamBot")
DEVELOPER_LINK = os.environ.get("DEVELOPER_LINK", "https://t.me/YourUsername")
ADMIN_IDS      = [int(x) for x in os.environ.get("ADMIN_IDS", "").split(",") if x.strip()]
WORKDIR        = os.environ.get("PYBOT_WORKDIR", ".")

# ── In-memory fsub state ──────────────────────────────────────────────────────
fsub_channel: int | None = None
fsub_channel_link: str | None = None

# ── Logging ───────────────────────────────────────────────────────────────────
logging.basicConfig(
    level=logging.INFO,
    format="%(asctime)s [PYBOT] %(levelname)s %(message)s",
    datefmt="%d/%m/%Y %I:%M:%S %p",
    stream=sys.stdout,
)
log = logging.getLogger("pybot")

# ── Kurigram client ───────────────────────────────────────────────────────────
app = Client(
    "fsb_python",
    api_id=API_ID,
    api_hash=API_HASH,
    bot_token=BOT_TOKEN,
    workdir=WORKDIR,
)

# ── Helpers ───────────────────────────────────────────────────────────────────

def btn(text: str, *, cb: str = None, url: str = None,
        style: ButtonStyle = ButtonStyle.DEFAULT) -> InlineKeyboardButton:
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
        log.warning("fsub check error for user %d: %s", user_id, e)
        return True


def fsub_markup() -> InlineKeyboardMarkup:
    rows = []
    if fsub_channel_link:
        rows.append([btn("📢 Join Channel", url=fsub_channel_link,
                         style=ButtonStyle.PRIMARY)])
    rows.append([btn("✅ I've Joined — Check Me", cb="check_sub",
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

# ── Admin-only decorator ──────────────────────────────────────────────────────

def admin_only(func):
    async def wrapper(client: Client, message: Message):
        if not ADMIN_IDS:
            await message.reply("⚠️ No ADMIN_IDS configured. Set ADMIN_IDS in your env.")
            return
        if message.from_user.id not in ADMIN_IDS:
            await message.reply("❌ You are not authorised to use this command.")
            return
        await func(client, message)
    wrapper.__name__ = func.__name__
    return wrapper

# ── /setfsub ──────────────────────────────────────────────────────────────────

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
        log.info("fsub set → %s (%s)", chat.title, chat.id)

    except (ChannelInvalid, PeerIdInvalid):
        await message.reply("❌ Channel not found. Make sure the bot is an admin there.")
    except ChatAdminRequired:
        await message.reply("❌ Bot needs admin rights in that channel.")
    except Exception as e:
        await message.reply(f"❌ Error: `{e}`")

# ── /removefsub ───────────────────────────────────────────────────────────────

@app.on_message(filters.command("removefsub") & filters.private)
@admin_only
async def cmd_remove_fsub(client: Client, message: Message):
    global fsub_channel, fsub_channel_link
    fsub_channel = None
    fsub_channel_link = None
    await message.reply("✅ Force-subscribe has been **disabled**.")
    log.info("fsub disabled")

# ── /fsub status ──────────────────────────────────────────────────────────────

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
        await message.reply(f"⚠️ fsub ID is `{fsub_channel}` but got error: `{e}`")

# ── File gate — block non-subscribers ────────────────────────────────────────

@app.on_message(
    filters.private
    & (filters.document | filters.video | filters.audio
       | filters.photo | filters.voice | filters.video_note)
)
async def gate_file(client: Client, message: Message):
    if fsub_channel is None:
        return  # fsub off — Go bot handles it
    if not await is_subscribed(client, message.from_user.id):
        await message.reply(
            "🔒 **Access Restricted**\n\n"
            "Please join our channel first, then send your file again.",
            reply_markup=fsub_markup(),
        )

# ── Entry point ───────────────────────────────────────────────────────────────

def _handle_signal(sig, frame):
    log.info("Signal %s received, shutting down...", sig)
    app.stop()
    sys.exit(0)


if __name__ == "__main__":
    signal.signal(signal.SIGTERM, _handle_signal)
    signal.signal(signal.SIGINT, _handle_signal)
    log.info("Python UI bot starting (API_ID=%d, site=%s)...", API_ID, _site or "env")
    app.run()
