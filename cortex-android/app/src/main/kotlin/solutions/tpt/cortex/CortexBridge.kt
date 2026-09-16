// Copyright 2026 TPT Solutions. Dual-licensed MIT OR Apache-2.0.
package solutions.tpt.cortex

import android.content.Context
import android.util.Log
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.Response
import okhttp3.WebSocket
import okhttp3.WebSocketListener
import java.util.concurrent.TimeUnit
import java.util.concurrent.atomic.AtomicBoolean

/**
 * WebSocket JSON-RPC bridge to the cortex-daemon, speaking the exact contract
 * documented in docs/jsonrpc-contract.md. The same payloads work from the
 * browser (cortex-client.ts) and from here -- one contract, three runtimes.
 */
class CortexBridge(
    @Suppress("unused") private val context: Context,
    private val url: String,
    private val listener: Listener,
) {
    enum class State { DISCONNECTED, CONNECTING, CONNECTED }

    interface Listener {
        fun onStateChange(state: State)
    }

    private val client = OkHttpClient.Builder()
        .pingInterval(20, TimeUnit.SECONDS)
        .connectTimeout(3, TimeUnit.SECONDS)
        .build()
    private var webSocket: WebSocket? = null
    private val started = AtomicBoolean(false)
    @Volatile private var state: State = State.DISCONNECTED

    private val socketListener = object : WebSocketListener() {
        override fun onOpen(webSocket: WebSocket, response: Response) {
            setState(State.CONNECTED)
            // Capability probe from the contract; response handling is a
            // next-milestone concern (task dispatch → service scheduler).
            webSocket.send("""{"jsonrpc":"2.0","id":1,"method":"cortex.ping"}""")
        }

        override fun onMessage(webSocket: WebSocket, text: String) {
            Log.d(TAG, "rpc <- $text")
        }

        override fun onFailure(webSocket: WebSocket, t: Throwable, response: Response?) {
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
    }
}
