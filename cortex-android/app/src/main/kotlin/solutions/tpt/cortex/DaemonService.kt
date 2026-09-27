// Copyright 2026 TPT Solutions. Dual-licensed MIT OR Apache-2.0.
package solutions.tpt.cortex

import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.Service
import android.content.Context
import android.content.Intent
import android.os.Binder
import android.os.IBinder
import android.util.Log
import java.util.concurrent.CopyOnWriteArraySet
import java.util.concurrent.atomic.AtomicBoolean
import org.json.JSONObject

/**
 * Foreground service hosting the tpt-cortex runtime on Android (spec §3
 * Layer 3, §7 distribution). Responsibilities:
 *
 *  1. process liveness via the foreground notification (Android won't kill
 *     background sync out from under the PWA);
 *  2. embedding the Go daemon when the gomobile .aar is packaged
 *     (see cortex-android README — `solutions.tpt.cortex.Mobile.start`);
 *  3. the JSON-RPC [CortexBridge] to ws://127.0.0.1:9911/rpc, whose daemon
 *     notifications are relayed to bound clients (MainActivity forwards them
 *     into the PWA's WebView as `cortex:notification` events).
 */
class DaemonService : Service() {

    private val running = AtomicBoolean(false)
    private val notificationListeners = CopyOnWriteArraySet<(method: String, params: JSONObject) -> Unit>()
    private val stateListeners = CopyOnWriteArraySet<(state: CortexBridge.State) -> Unit>()
    private lateinit var bridge: CortexBridge

    inner class LocalBinder : Binder() {
        fun addNotificationListener(listener: (method: String, params: JSONObject) -> Unit) {
            notificationListeners.add(listener)
        }

        fun removeNotificationListener(listener: (method: String, params: JSONObject) -> Unit) {
            notificationListeners.remove(listener)
        }

        fun addStateListener(listener: (state: CortexBridge.State) -> Unit) {
            stateListeners.add(listener)
        }

        fun removeStateListener(listener: (state: CortexBridge.State) -> Unit) {
            stateListeners.remove(listener)
        }

        val cortexBridge: CortexBridge
            get() = this@DaemonService.bridge
    }

    private val binder = LocalBinder()

    override fun onCreate() {
        super.onCreate()
        bridge = CortexBridge(
            url = CortexBridge.DEFAULT_URL,
            listener = object : CortexBridge.Listener {
                override fun onStateChange(state: CortexBridge.State) {
                    Log.d(TAG, "daemon link: $state")
                    for (listener in stateListeners) listener(state)
                }

                override fun onNotification(method: String, params: JSONObject) {
                    Log.d(TAG, "daemon notification: $method")
                    for (listener in notificationListeners) listener(method, params)
                }
            },
        )
    }

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        if (running.compareAndSet(false, true)) {
            startForegroundCompat()
            startEmbeddedDaemon()
            bridge.start()
        }
        return START_STICKY // persistence is the whole point (spec §4)
    }

    override fun onDestroy() {
        if (running.compareAndSet(true, false)) {
            bridge.stop()
            stopEmbeddedDaemon()
        }
        super.onDestroy()
    }

    override fun onBind(intent: Intent?): IBinder = binder

    /**
     * Starts the gomobile-bound Go daemon when it was packaged into the APK
     * (`app/libs/cortex.aar`). Without the .aar this logs and moves on: the
     * PWA keeps working in degraded mode, and the bridge reconnects for as
     * long as the service lives.
     */
    private fun startEmbeddedDaemon(): Boolean = try {
        val mobile = Class.forName(EMBEDDED_DAEMON_CLASS)
        val start = mobile.getMethod("start", String::class.java, String::class.java, String::class.java)
        val queueDir = getDir("cortex-queue", Context.MODE_PRIVATE).absolutePath
        start.invoke(null, CortexBridge.DEFAULT_URL.removePrefix("ws://").removeSuffix("/rpc"), queueDir, null)
        Log.i(TAG, "embedded cortex-daemon started (queue: $queueDir)")
        true
    } catch (t: Throwable) {
        Log.i(TAG, "embedded daemon not packaged (build cortex.aar — see README): ${t.message}")
        false
    }

    private fun stopEmbeddedDaemon() {
        try {
            Class.forName(EMBEDDED_DAEMON_CLASS).getMethod("stop").invoke(null)
        } catch (_: Throwable) {
            // nothing embedded: fine
        }
    }

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
        private const val TAG = "DaemonService"
        private const val CHANNEL_ID = "cortex-daemon"
        private const val NOTIFICATION_ID = 0xC07E

        /** gomobile bind -javapkg=solutions.tpt.cortex ./mobile produces this class. */
        private const val EMBEDDED_DAEMON_CLASS = "solutions.tpt.cortex.Mobile"

        /** Convenience start entry point used by MainActivity. */
        fun start(context: Context) {
            context.startForegroundService(Intent(context, DaemonService::class.java))
        }
    }
}
