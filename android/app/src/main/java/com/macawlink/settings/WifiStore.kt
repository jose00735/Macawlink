package com.macawlink.settings

import android.content.Context
import com.macawlink.protocol.ProtoJson
import com.macawlink.session.SessionHub
import kotlinx.serialization.Serializable
import kotlinx.serialization.decodeFromString
import kotlinx.serialization.encodeToString
import java.util.UUID

/** Phone identity, paired PCs (pinned certificate + token) and the last Wi-Fi target. */
class WifiStore(context: Context) {
    private val prefs = context.getSharedPreferences("macawlink-wifi", Context.MODE_PRIVATE)

    @Serializable
    data class PairedPc(val fp: String, val name: String, val token: String)

    @Serializable
    private data class Target(val host: String, val port: Int, val name: String, val shortFp: String = "")

    val phoneId: String
        get() = prefs.getString("phone_id", null) ?: UUID.randomUUID().toString().also {
            prefs.edit().putString("phone_id", it).apply()
        }

    fun paired(): List<PairedPc> = prefs.getString("paired", null)
        ?.let { runCatching { ProtoJson.decodeFromString<List<PairedPc>>(it) }.getOrNull() } ?: emptyList()

    /** Paired PC whose short fingerprint (mDNS TXT "fp") matches, if any. */
    fun pairedFor(shortFp: String): PairedPc? = if (shortFp.isEmpty()) null else paired().firstOrNull { it.fp.startsWith(shortFp) }

    fun savePaired(pc: PairedPc) {
        val list = paired().filterNot { it.fp == pc.fp } + pc
        prefs.edit().putString("paired", ProtoJson.encodeToString(list)).apply()
    }

    fun forget(fp: String) {
        prefs.edit().putString("paired", ProtoJson.encodeToString(paired().filterNot { it.fp == fp })).apply()
    }

    fun lastTarget(): SessionHub.WifiTarget? = prefs.getString("target", null)?.let {
        runCatching { ProtoJson.decodeFromString<Target>(it) }.getOrNull()
    }?.let { SessionHub.WifiTarget(it.host, it.port, it.name, it.shortFp) }

    fun saveTarget(t: SessionHub.WifiTarget?) {
        val e = prefs.edit()
        if (t == null) e.remove("target") else e.putString("target", ProtoJson.encodeToString(Target(t.host, t.port, t.name, t.shortFp)))
        e.apply()
    }
}
