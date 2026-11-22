package com.macawlink

import com.macawlink.protocol.HelloMsg
import com.macawlink.protocol.MsgType
import com.macawlink.protocol.Packet
import com.macawlink.protocol.PrompterKeyMsg
import com.macawlink.protocol.PrompterScript
import com.macawlink.protocol.PrompterState
import com.macawlink.protocol.ProtoJson
import com.macawlink.protocol.readPacket
import com.macawlink.protocol.writePacket
import com.macawlink.ui.LensEdge
import com.macawlink.ui.lensEdge
import com.macawlink.ui.stressed
import kotlinx.serialization.json.JsonObject
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test
import java.io.ByteArrayInputStream
import java.io.ByteArrayOutputStream
import java.io.DataInputStream
import java.io.DataOutputStream
import java.io.File

/** Teleprompter feed (TeleCyborg → Macawlink PC → phone), fixtures in proto-golden/prompter.json. */
class PrompterTest {
    private val fixture = ProtoJson.parseToJsonElement(File("../../proto-golden/prompter.json").readText()) as JsonObject

    @Test
    fun decodesFixtures() {
        val s = ProtoJson.decodeFromJsonElement(PrompterScript.serializer(), fixture["script"]!!)
        assertEquals("terrific-pitch", s.id)
        assertEquals(3, s.blocks.size)
        assertEquals("para", s.blocks[1].k)
        assertEquals(1.25, s.fontScale, 1e-9)
        val st = ProtoJson.decodeFromJsonElement(PrompterState.serializer(), fixture["state"]!!)
        assertEquals(0.5, st.frac, 1e-9)
        assertTrue(st.playing)
        assertEquals("behind", st.alert)
        assertEquals("next", ProtoJson.decodeFromJsonElement(PrompterKeyMsg.serializer(), fixture["key"]!!).key)
    }

    @Test
    fun helloAnnouncesPrompter() {
        val js = ProtoJson.encodeToString(HelloMsg.serializer(), HelloMsg("m", "x", "1", cameras = emptyList(), features = listOf("prompter")))
        assertTrue(js.contains("\"features\":[\"prompter\"]"))
    }

    @Test
    fun unknownTypesAreSkippedNotFatal() {
        val bos = ByteArrayOutputStream()
        val out = DataOutputStream(bos)
        out.writePacket(Packet(MsgType.PING, payload = ByteArray(8)))
        bos.toByteArray().also { it[3] = 0x77 } // a future message type
            .let { DataInputStream(ByteArrayInputStream(it)).readPacket() }
            .also { assertEquals(MsgType.UNKNOWN, it.type); assertEquals(8, it.payload.size) }
    }

    @Test
    fun lensSideAndStress() {
        assertEquals(LensEdge.LEFT, lensEdge(android.view.Surface.ROTATION_90))
        assertEquals(LensEdge.TOP, lensEdge(android.view.Surface.ROTATION_0))
        val a = stressed("Hi **Jose** there")
        assertEquals("Hi Jose there", a.text)
        assertEquals(1, a.spanStyles.size)
    }
}
