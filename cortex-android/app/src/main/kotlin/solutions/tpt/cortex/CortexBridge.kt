// Copyright 2026 TPT Solutions. Dual-licensed MIT OR Apache-2.0.
package solutions.tpt.cortex

import android.util.Log
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.Response
import okhttp3.WebSocket
import okhttp3.WebSocketListener
import org.json.JSONArray
import org.json.JSONObject
import java.util.concurrent.ConcurrentHashMap
import java.util.concurrent.TimeUnit
import java.util.concurrent.atomic.AtomicBoolean
import java.util.concurrent.atomic.AtomicLong

/**
 * JSON-RPC 2.0 client for the cortex-daemon, speaking the exact contract in
 * docs/jsonrpc-contract.md. The same payloads work from the browser
 * (cortex-client.ts) and from here -- one contract, three runtimes.
 *
 * Two relay paths exist on Android:
 *  1. the PWA inside the WebView connects DIRECTLY to ws://127.0.0.1:9911
 *     (the network security config permits loopback cleartext); and
 *  2. this bridge supervises the daemon from the service side and forwards
 *     daemon notifications into the WebView as `cortex:notification` DOM
 *     events, so the PWA can react even if its own socket is between
 *     reconnects.
 */
class CortexBridge(
    private val url: String,
    private val listener: Listener,
) {
    enum class State { DISCONNECTED, CONNECTING, CONNECTED }

    interface Listener {
        fun onStateChange(state: State)
        fun onNotification(method: String, params: JSONObject)
    }

    private class PendingCall(val onComplete: (JSONObject?) -> Unit, val onError: (String) -> Unit)

    private val client: OkHttpClient by lazy {
        OkHttpClient.Builder()
            .pingInterval(20, TimeUnit.SECONDS)
            .connectTimeout(3, TimeUnit.SECONDS)
            .build()
    }
    private var webSocket: WebSocket? = null
    private val pending = ConcurrentHashMap<Int, PendingCall>()
    private val nextId = AtomicLong(1)
    private val started = AtomicBoolean(false)
    @Volatile private var state: State = State.DISCONNECTED

    /** Send seam: tests substitute this to capture frames without a socket. */
    internal var sendOverride: ((String) -> Boolean)? = null

    private val socketListener = object : WebSocketListener() {
        override fun onOpen(webSocket: WebSocket, response: Response) {
            setState(State.CONNECTED)
            call("cortex.ping", null, onComplete = { /* daemon alive; the PWA does its own handshake */ }, onError = {})
        }

        override fun onMessage(webSocket: WebSocket, text: String) {
            onMessage(text)
        }

        override fun onFailure(webSocket: WebSocket, t: Throwable, response: Response?) {
            Log.w(TAG, "bridge failure: ${t.message}")
            setState(State.DISCONNECTED)
            scheduleReconnect()
        }

        override fun onClosed(webSocket: WebSocket, code: Int, reason: String) {
            setState(State.DISCONNECTED)
        }
    }

    fun start() {
        if (!started.compareAndSet(false, true)) return
        connect()
    }

    fun stop() {
        started.set(false)
        webSocket?.close(NORMAL_CLOSE, "service stopping")
        webSocket = null
        setState(State.DISCONNECTED)
    }

    /** Fire a JSON-RPC request; exactly one of the callbacks will be invoked. */
    fun call(method: String, params: JSONObject?, onComplete: (JSONObject?) -> Unit, onError: (String) -> Unit) {
        if (state != State.CONNECTED) {
            onError("daemon not connected")
            return
        }
        val id = nextId.getAndIncrement().toInt()
        pending[id] = PendingCall(onComplete, onError)
        val request = JSONObject().apply {
            put("jsonrpc", "2.0")
            put("id", id)
            put("method", method)
            if (params != null) put("params", params)
        }
        val send = sendOverride ?: webSocket?.let { socket -> { message: String -> socket.send(message) } }
        if (send == null || !send(request.toString())) {
            pending.remove(id)
            onError("socket send failed")
        }
    }

    /** Contract helper: queue the PWA's sync outbox with the daemon (spec §4 Path A). */
    fun enqueueSync(entries: JSONArray, onComplete: (JSONObject?) -> Unit, onError: (String) -> Unit) {
        val params = JSONObject().apply {
            put("kind", "syncNotes")
            put("entries", entries)
        }
        call("cortex.task.enqueue", params, onComplete, onError)
    }

    /** Single message entry point; visible for tests. */
    fun onMessage(text: String) {
        val message = runCatching { JSONObject(text) }.getOrNull() ?: return
        if (message.has("id")) {
            val id = message.optInt("id", -1)
            val waiting = pending.remove(id) ?: return
            val error = message.optJSONObject("error")
            if (error != null) {
                waiting.onError(error.optString("message", "cortex RPC error"))
            } else {
                waiting.onComplete(message.optJSONObject("result"))
            }
            return
        }
        val method = message.optString("method")
        if (method.isNotEmpty()) {
            listener.onNotification(method, message.optJSONObject("params") ?: JSONObject())
        }
    }

    /** Test seam: pretend the socket handshake completed. */
    internal fun markConnected() {
        setState(State.CONNECTED)
    }

    private fun connect() {
        if (!started.get()) return
        setState(State.CONNECTING)
        val request = Request.Builder().url(url).build()
        webSocket = client.newWebSocket(request, socketListener)
    }

    private fun scheduleReconnect() {
        if (!started.get()) return
        // Fixed backoff for the skeleton; jittered exponential later.
        Thread {
            try {
                Thread.sleep(RECONNECT_MS)
            } catch (_: InterruptedException) {
                return@Thread
            }
            connect()
        }.start()
    }

    private fun setState(next: State) {
        if (state != next) {
            state = next
            listener.onStateChange(next)
        }
    }

    companion object {
        private const val TAG = "CortexBridge"
        private const val NORMAL_CLOSE = 1000
        private const val RECONNECT_MS = 5_000L
        const val DEFAULT_URL = "ws://127.0.0.1:9911/rpc"

        /** "ws://127.0.0.1:9911/rpc" -> "127.0.0.1:9911" (the Mobile.start addr). */
        fun hostOf(url: String): String = url
            .removePrefix("wss://")
            .removePrefix("ws://")
            .substringBefore('/')
    }
}
