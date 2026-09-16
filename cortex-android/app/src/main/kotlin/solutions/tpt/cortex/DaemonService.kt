// Copyright 2026 TPT Solutions. Dual-licensed MIT OR Apache-2.0.
package solutions.tpt.cortex

import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.Service
import android.content.Context
import android.content.Intent
import android.os.IBinder
import java.util.concurrent.atomic.AtomicBoolean

/**
 * Foreground service hosting the tpt-cortex runtime on Android (spec §3
 * Layer 3, §7 distribution). Milestone roadmap, in order:
 *
 *  1. THIS SKELETON: process liveness + JSON-RPC bridge loop. The service
 *     holds the foreground notification that keeps Android from killing
 *     background sync, and connects the [CortexBridge] to the daemon's
 *     ws://127.0.0.1:9911/rpc endpoint.
 *  2. NEXT: embed cortex-daemon via gomobile bind; the service lifecycle
 *     below stays identical, only `daemonHandle` gains a real implementation.
 */
class DaemonService : Service() {

    private val running = AtomicBoolean(false)
    private lateinit var bridge: CortexBridge

    override fun onCreate() {
        super.onCreate()
        bridge = CortexBridge(
            context = applicationContext,
            url = CortexBridge.DEFAULT_URL,
            listener = object : CortexBridge.Listener {
                override fun onStateChange(state: CortexBridge.State) {
                    // TODO: broadcast to the PWA via the bridge once the
                    // daemon wires taskCompleted notifications through.
                }
            },
        )
    }

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        if (running.compareAndSet(false, true)) {
            startForegroundCompat()
            bridge.start()
        }
        return START_STICKY // persistence is the whole point (spec §4)
    }

    override fun onDestroy() {
        if (running.compareAndSet(true, false)) {
            bridge.stop()
        }
        super.onDestroy()
    }

    override fun onBind(intent: Intent?): IBinder? = null

    private fun startForegroundCompat() {
        val manager = getSystemService(Context.NOTIFICATION_SERVICE) as NotificationManager
        manager.createNotificationChannel(
            NotificationChannel(CHANNEL_ID, getString(R.string.daemon_notification_channel), NotificationManager.IMPORTANCE_LOW),
        )
        val notification: Notification =
            Notification.Builder(this, CHANNEL_ID)
                .setContentTitle(getString(R.string.daemon_notification_title))
                .setContentText(getString(R.string.daemon_notification_text))
                .setSmallIcon(android.R.drawable.stat_notify_sync)
                .setOngoing(true)
                .build()
        startForeground(NOTIFICATION_ID, notification)
    }

    companion object {
        private const val CHANNEL_ID = "cortex-daemon"
        private const val NOTIFICATION_ID = 0xC07E

        /** Convenience start entry point used by MainActivity. */
        fun start(context: Context) {
            context.startForegroundService(Intent(context, DaemonService::class.java))
        }
    }
}
