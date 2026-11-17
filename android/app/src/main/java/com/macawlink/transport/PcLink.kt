package com.macawlink.transport

import com.macawlink.protocol.MsgType
import com.macawlink.protocol.Packet
import com.macawlink.protocol.Proto
import com.macawlink.protocol.readPacket
import com.macawlink.protocol.writePacket
import java.io.BufferedInputStream
import java.io.BufferedOutputStream
import java.io.DataInputStream
import java.io.DataOutputStream
import java.net.InetAddress
import java.net.InetSocketAddress
import java.net.ServerSocket
import java.net.Socket
import java.util.concurrent.LinkedBlockingQueue
import java.util.concurrent.TimeUnit
import java.util.concurrent.atomic.AtomicInteger
import java.util.concurrent.atomic.AtomicLong
import kotlin.concurrent.thread

/**
 * USB mode: listens on the phone's loopback; the PC runs `adb forward tcp:47800 tcp:47800`
 * and connects through the cable. Nothing is reachable from the network.
 */
class PcLink(private val port: Int = Proto.DEFAULT_PORT) {
    private var server: ServerSocket? = null

    fun open() {
        server = ServerSocket().apply {
            reuseAddress = true
            bind(InetSocketAddress(InetAddress.getLoopbackAddress(), port))
        }
    }

    fun accept(): Connection = Connection(server!!.accept())

    fun close() {
        runCatching { server?.close() }
        server = null
    }
}

/** One PC connection. Sends go through a writer thread so encoders never block on the socket. */
class Connection(private val socket: Socket) {
    private val out = DataOutputStream(BufferedOutputStream(socket.getOutputStream(), 256 * 1024))
    private val inp = DataInputStream(BufferedInputStream(socket.getInputStream(), 64 * 1024))
    private val queue = LinkedBlockingQueue<Packet>()
    private val queuedBytes = AtomicLong()
    private val seq = AtomicInteger()
    val sentBytes = AtomicLong()

    @Volatile var closed = false
        private set

    init {
        socket.tcpNoDelay = true
        socket.sendBufferSize = 1 shl 20
        thread(name = "mk-writer", isDaemon = true) {
            try {
                while (!closed) {
                    val p = queue.poll(250, TimeUnit.MILLISECONDS) ?: continue
                    out.writePacket(p)
                    queuedBytes.addAndGet(-p.payload.size.toLong())
                    sentBytes.addAndGet(p.payload.size + 16L)
                    if (queue.isEmpty()) out.flush()
                }
            } catch (_: Exception) {
                close()
            }
        }
    }

    fun send(type: MsgType, payload: ByteArray = ByteArray(0), flags: Int = 0) {
        if (closed) return
        queuedBytes.addAndGet(payload.size.toLong())
        queue.offer(Packet(type, flags, seq.incrementAndGet(), payload))
    }

    /** Video may be dropped when the link is congested; returns false if it was. */
    fun sendVideo(payload: ByteArray, flags: Int): Boolean {
        if (queuedBytes.get() > MAX_QUEUED_VIDEO) return false
        send(MsgType.VIDEO_FRAME, payload, flags)
        return true
    }

    fun read(): Packet = inp.readPacket()

    fun close() {
        if (closed) return
        closed = true
        runCatching { socket.close() }
    }

    companion object {
        const val MAX_QUEUED_VIDEO = 3L * 1024 * 1024
    }
}
