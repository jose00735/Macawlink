package com.macawlink.transport

import java.net.InetSocketAddress
import java.security.MessageDigest
import java.security.SecureRandom
import java.security.cert.CertificateException
import java.security.cert.X509Certificate
import javax.net.ssl.SSLContext
import javax.net.ssl.SSLSocket
import javax.net.ssl.X509TrustManager

/**
 * TLS 1.3 client to the PC. The PC's certificate is self-signed, so trust comes from pairing:
 * first time any certificate is accepted and the pairing code (derived from its fingerprint)
 * is compared by the user; afterwards the fingerprint is pinned.
 */
object WifiDialer {
    class Dialed(val connection: Connection, val certFp: ByteArray)

    fun dial(host: String, port: Int, pinnedFp: ByteArray?, timeoutMs: Int = 5000): Dialed {
        var seen: ByteArray? = null
        // Not a CA check on purpose: the PC is self-signed. Trust comes from the pairing code
        // (bound to this certificate's fingerprint) and from pinning that fingerprint afterwards.
        @android.annotation.SuppressLint("CustomX509TrustManager")
        val tm = object : X509TrustManager {
            override fun checkClientTrusted(chain: Array<out X509Certificate>?, authType: String?) = throw CertificateException()
            override fun checkServerTrusted(chain: Array<out X509Certificate>, authType: String?) {
                val fp = MessageDigest.getInstance("SHA-256").digest(chain[0].encoded)
                if (pinnedFp != null && !MessageDigest.isEqual(fp, pinnedFp)) {
                    throw CertificateException("PC certificate changed — pair again")
                }
                seen = fp
            }
            override fun getAcceptedIssuers(): Array<X509Certificate> = emptyArray()
        }
        val ctx = SSLContext.getInstance("TLSv1.3").apply { init(null, arrayOf(tm), SecureRandom()) }
        val raw = java.net.Socket()
        raw.connect(InetSocketAddress(host, port), timeoutMs)
        val s = ctx.socketFactory.createSocket(raw, host, port, true) as SSLSocket
        s.enabledProtocols = arrayOf("TLSv1.3")
        s.soTimeout = 15_000
        s.startHandshake()
        s.soTimeout = 0
        return Dialed(Connection(s), seen ?: throw CertificateException("no certificate"))
    }
}
