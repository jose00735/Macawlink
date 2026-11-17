package com.macawlink.protocol

import java.io.DataInputStream
import java.io.DataOutputStream
import java.io.IOException

/** Wire format from docs/PROTOCOL.md — keep in sync with windows/internal/protocol. */
object Proto {
    const val MAGIC: Int = 0x4D4B
    const val VERSION: Int = 1
    const val HEADER_SIZE = 16
    const val MAX_PAYLOAD = 4 shl 20
    const val DEFAULT_PORT = 47800 // USB: we listen on loopback
    const val WIFI_PORT = 47802    // Wi-Fi: the PC listens (TLS 1.3)
    const val MDNS_SERVICE = "_macawlink._tcp."

    const val FLAG_KEYFRAME = 1
    const val FLAG_CONFIG = 2
    const val FLAG_END_OF_FRAME = 4
}

enum class MsgType(val code: Int) {
    HELLO(0x01), HELLO_ACK(0x02),
    VIDEO_CONFIG(0x10), VIDEO_FRAME(0x11),
    AUDIO_CONFIG(0x20), AUDIO_FRAME(0x21),
    SET_CONFIG(0x30), CONFIG_STATE(0x31), REQUEST_KEYFRAME(0x32), CAMERA_CONTROL(0x33),
    PING(0x40), PONG(0x41), STATS(0x42),
    PAIR_BEGIN(0x50), PAIR_NONCE(0x51), PAIR_REVEAL(0x52), PAIR_CONFIRM(0x53), PAIR_RESULT(0x54),
    BYE(0x7F);

    companion object {
        private val byCode = entries.associateBy { it.code }
        fun of(code: Int) = byCode[code] ?: throw IOException("unknown type $code")
    }
}

class Packet(val type: MsgType, val flags: Int = 0, val seq: Int = 0, val payload: ByteArray = ByteArray(0))

/** Not thread-safe: wrap writes in a single writer coroutine/lock. */
fun DataOutputStream.writePacket(p: Packet) {
    require(p.payload.size <= Proto.MAX_PAYLOAD)
    writeShort(Proto.MAGIC)
    writeByte(Proto.VERSION)
    writeByte(p.type.code)
    writeByte(p.flags)
    write(ByteArray(3))
    writeInt(p.seq)
    writeInt(p.payload.size)
    write(p.payload)
}

fun DataInputStream.readPacket(): Packet {
    if (readUnsignedShort() != Proto.MAGIC) throw IOException("bad magic")
    val version = readUnsignedByte()
    if (version != Proto.VERSION) throw IOException("unsupported version $version")
    val type = MsgType.of(readUnsignedByte())
    val flags = readUnsignedByte()
    skipBytes(3)
    val seq = readInt()
    val len = readInt()
    if (len < 0 || len > Proto.MAX_PAYLOAD) throw IOException("payload too large")
    val payload = ByteArray(len).also { readFully(it) }
    return Packet(type, flags, seq, payload)
}

/** Media payload = 8-byte big-endian pts (µs) + data. */
fun mediaPayload(ptsUs: Long, data: ByteArray, offset: Int = 0, length: Int = data.size): ByteArray {
    val out = ByteArray(8 + length)
    for (i in 0 until 8) out[i] = (ptsUs ushr (56 - 8 * i)).toByte()
    System.arraycopy(data, offset, out, 8, length)
    return out
}
