package com.macawlink

import com.macawlink.capture.CameraController
import com.macawlink.protocol.MsgType
import com.macawlink.protocol.Packet
import com.macawlink.protocol.Proto
import com.macawlink.protocol.ProtoJson
import com.macawlink.protocol.mediaPayload
import com.macawlink.protocol.readPacket
import com.macawlink.protocol.writePacket
import com.macawlink.settings.StreamSettings
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonObject
import org.junit.Assert.assertArrayEquals
import org.junit.Assert.assertEquals
import org.junit.Test
import java.io.ByteArrayInputStream
import java.io.ByteArrayOutputStream
import java.io.DataInputStream
import java.io.DataOutputStream
import java.io.File

/** Same fixtures as windows/internal/protocol/golden_test.go. */
class GoldenTest {
    private val golden = File("../../proto-golden")

    @Test
    fun packetBytesMatchGo() {
        val bos = ByteArrayOutputStream()
        DataOutputStream(bos).writePacket(Packet(MsgType.VIDEO_FRAME, Proto.FLAG_KEYFRAME, 42, mediaPayload(123456, byteArrayOf(0, 0, 1))))
        val hex = bos.toByteArray().joinToString("") { "%02x".format(it) }
        assertEquals(File(golden, "video_frame_packet.hex").readText().trim(), hex)

        val back = DataInputStream(ByteArrayInputStream(bos.toByteArray())).readPacket()
        assertEquals(MsgType.VIDEO_FRAME, back.type)
        assertEquals(42, back.seq)
        assertArrayEquals(mediaPayload(123456, byteArrayOf(0, 0, 1)), back.payload)
    }

    @Test
    fun defaultSettingsJsonMatchesGo() {
        val ours = Json.parseToJsonElement(ProtoJson.encodeToString(StreamSettings.serializer(), StreamSettings())) as JsonObject
        val theirs = Json.parseToJsonElement(File(golden, "settings.json").readText()) as JsonObject
        assertEquals(theirs.toString(), ours.toString())
        assertEquals(StreamSettings(), ProtoJson.decodeFromString(StreamSettings.serializer(), File(golden, "settings.json").readText()))
    }

    @Test
    fun picksSizesAndFps() {
        val sizes = listOf(1920 to 1080, 1280 to 720, 640 to 480, 1440 to 1080)
        assertEquals(1280 to 720, CameraController.pickSize(sizes, 1280, 720))
        assertEquals(1920 to 1080, CameraController.pickSize(sizes, 1920, 1088)) // same 16:9 aspect, nearest area
        assertEquals(640 to 480, CameraController.pickSize(listOf(1920 to 1080, 640 to 480), 800, 600)) // 4:3 kept
    }
}

class PairingGoldenTest {
    @org.junit.Test
    fun sasMatchesGo() {
        val g = kotlinx.serialization.json.Json.parseToJsonElement(java.io.File("../../proto-golden/pairing.json").readText())
            as kotlinx.serialization.json.JsonObject
        fun s(k: String) = (g[k] as kotlinx.serialization.json.JsonPrimitive).content
        val fp = com.macawlink.protocol.Pairing.unhex(s("cert_fp"))
        val pc = com.macawlink.protocol.Pairing.unhex(s("pc_nonce"))
        val ph = com.macawlink.protocol.Pairing.unhex(s("phone_nonce"))
        org.junit.Assert.assertEquals(s("sas"), com.macawlink.protocol.Pairing.sas(fp, pc, ph))
        org.junit.Assert.assertEquals(s("commit"), com.macawlink.protocol.Pairing.commit(pc))
    }

    @Test fun rotationHysteresis() {
        val q = CameraController::quadrantWithHysteresis
        assertEquals(0, q(50, 0))     // just past 45°: stay
        assertEquals(0, q(310, 0))
        assertEquals(90, q(70, 0))    // clearly turned
        assertEquals(270, q(290, 0))
        assertEquals(90, q(30, 90))   // jitter back toward 0 is ignored
        assertEquals(0, q(20, 90))
        assertEquals(180, q(180, 0))
        assertEquals(0, q(359, 0))
    }
}
