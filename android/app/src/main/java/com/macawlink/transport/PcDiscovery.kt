package com.macawlink.transport

import android.content.Context
import android.net.nsd.NsdManager
import android.net.nsd.NsdServiceInfo
import android.net.wifi.WifiManager
import android.os.Build
import android.util.Log
import com.macawlink.session.SessionHub
import kotlinx.coroutines.flow.MutableStateFlow
import java.util.concurrent.Executors

/** Finds Macawlink PCs on the LAN via mDNS (_macawlink._tcp). */
class PcDiscovery(context: Context) {
    private val nsd = context.getSystemService(NsdManager::class.java)
    private val wifi = context.applicationContext.getSystemService(WifiManager::class.java)
    private var lock: WifiManager.MulticastLock? = null
    private var listener: NsdManager.DiscoveryListener? = null
    private val resolver = Executors.newSingleThreadExecutor()

    val found = MutableStateFlow<List<SessionHub.WifiTarget>>(emptyList())

    fun start() {
        if (listener != null) return
        lock = wifi.createMulticastLock("macawlink-mdns").apply { setReferenceCounted(false); acquire() }
        val l = object : NsdManager.DiscoveryListener {
            override fun onDiscoveryStarted(serviceType: String) = Unit
            override fun onDiscoveryStopped(serviceType: String) = Unit
            override fun onStartDiscoveryFailed(serviceType: String, errorCode: Int) { Log.w(TAG, "discovery failed $errorCode") }
            override fun onStopDiscoveryFailed(serviceType: String, errorCode: Int) = Unit
            override fun onServiceFound(info: NsdServiceInfo) { resolver.execute { resolve(info) } }
            override fun onServiceLost(info: NsdServiceInfo) {
                found.value = found.value.filterNot { it.name == info.serviceName.removePrefix("Macawlink on ") }
            }
        }
        listener = l
        runCatching { nsd.discoverServices("_macawlink._tcp", NsdManager.PROTOCOL_DNS_SD, l) }
            .onFailure { Log.w(TAG, "discoverServices", it) }
    }

    @Suppress("DEPRECATION") // resolveService is fine for our one-shot lookups
    private fun resolve(info: NsdServiceInfo) {
        val done = java.util.concurrent.CountDownLatch(1)
        nsd.resolveService(info, object : NsdManager.ResolveListener {
            override fun onResolveFailed(si: NsdServiceInfo, errorCode: Int) { done.countDown() }
            override fun onServiceResolved(si: NsdServiceInfo) {
                val host = if (Build.VERSION.SDK_INT >= 34) si.hostAddresses.firstOrNull()?.hostAddress else si.host?.hostAddress
                val attrs = si.attributes
                val name = attrs["name"]?.decodeToString() ?: si.serviceName.removePrefix("Macawlink on ")
                val fp = attrs["fp"]?.decodeToString() ?: ""
                if (host != null) {
                    val t = SessionHub.WifiTarget(host, si.port, name, fp)
                    found.value = (found.value.filterNot { it.name == name } + t).sortedBy { it.name }
                }
                done.countDown()
            }
        })
        done.await(5, java.util.concurrent.TimeUnit.SECONDS) // NsdManager resolves one at a time
    }

    fun stop() {
        listener?.let { runCatching { nsd.stopServiceDiscovery(it) } }
        listener = null
        lock?.let { if (it.isHeld) it.release() }
        lock = null
    }

    private companion object { const val TAG = "MacawlinkDiscovery" }
}
