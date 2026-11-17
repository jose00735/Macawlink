package com.macawlink.transport

import com.macawlink.protocol.Packet
import com.macawlink.protocol.Proto
import com.macawlink.protocol.readPacket
import com.macawlink.protocol.writePacket
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.flow
import kotlinx.coroutines.flow.flowOn
import java.io.BufferedInputStream
import java.io.BufferedOutputStream
import java.io.DataInputStream
import java.io.DataOutputStream
import java.net.InetAddress
import java.net.ServerSocket
import java.net.Socket

interface Transport {
    suspend fun connect()
    fun send(p: Packet)
    fun incoming(): Flow<Packet>
    fun close()
}

/**
 * USB mode: the phone listens on localhost; the PC runs `adb forward tcp:47800 tcp:47800`
 * and connects. No network permission prompts, lowest latency.
 */
class UsbTransport(private val port: Int = Proto.DEFAULT_PORT) : Transport {
    private var server: ServerSocket? = null
    private var socket: Socket? = null
    private lateinit var out: DataOutputStream
    private lateinit var inp: DataInputStream
    private val writeLock = Any()

    override suspend fun connect() {
        val ss = ServerSocket(port, 1, InetAddress.getLoopbackAddress()).also { server = it }
        val s = ss.accept().apply { tcpNoDelay = true }
        socket = s
        out = DataOutputStream(BufferedOutputStream(s.getOutputStream(), 64 * 1024))
        inp = DataInputStream(BufferedInputStream(s.getInputStream()))
    }

    override fun send(p: Packet) = synchronized(writeLock) {
        out.writePacket(p)
        out.flush()
    }

    override fun incoming(): Flow<Packet> = flow {
        while (true) emit(inp.readPacket())
    }.flowOn(Dispatchers.IO)

    override fun close() {
        runCatching { socket?.close() }
        runCatching { server?.close() }
    }
}

// WifiTransport (Phase 2): discover PC via NsdManager (_macawlink._tcp), TLS 1.3 with the
// certificate fingerprint pinned from the pairing QR code.
