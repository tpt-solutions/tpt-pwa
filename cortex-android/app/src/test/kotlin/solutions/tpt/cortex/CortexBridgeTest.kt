// Copyright 2026 TPT Solutions. Dual-licensed MIT OR Apache-2.0.
package solutions.tpt.cortex

import org.json.JSONArray
import org.json.JSONObject
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * Pure-JVM tests for the JSON-RPC framing of [CortexBridge] (docs
 * /jsonrpc-contract.md): no sockets, no Android runtime.
 */
class CortexBridgeTest {

    private class RecordingListener : CortexBridge.Listener {
        val notifications = mutableListOf<Pair<String, JSONObject>>()
        val states = mutableListOf<CortexBridge.State>()

        override fun onStateChange(state: CortexBridge.State) {
            states.add(state)
        }

        override fun onNotification(method: String, params: JSONObject) {
            notifications.add(method to params)
        }
    }

    private fun newBridge(listener: RecordingListener): CortexBridge {
        val bridge = CortexBridge(CortexBridge.DEFAULT_URL, listener)
        bridge.markConnected()
        return bridge
    }

    @Test
    fun `call frames a json-rpc request and resolves on response`() {
        val listener = RecordingListener()
        val bridge = newBridge(listener)
        val sent = mutableListOf<String>()
        bridge.sendOverride = { frame ->
            sent.add(frame)
            true
        }

        var completed: JSONObject? = null
        var error: String? = null
        val params = JSONObject().put("taskId", "t-1")
        bridge.call("cortex.task.status", params, onComplete = { completed = it }, onError = { error = it })

        assertEquals(1, sent.size)
        val frame = JSONObject(sent.single())
        assertEquals("2.0", frame.getString("jsonrpc"))
        assertEquals("cortex.task.status", frame.getString("method"))
        assertEquals("t-1", frame.getJSONObject("params").getString("taskId"))
        assertTrue(frame.has("id"))

        val response = JSONObject().put("id", frame.getInt("id")).put("result", JSONObject().put("state", "queued"))
        bridge.onMessage(response.toString())

        assertEquals("queued", completed?.getString("state"))
        assertEquals(null, error)
    }

    @Test
    fun `error response is surfaced through onerror`() {
        val listener = RecordingListener()
        val bridge = newBridge(listener)
        var frameId = -1
        bridge.sendOverride = { frame ->
            frameId = JSONObject(frame).getInt("id")
            true
        }

        var failed: String? = null
        bridge.call("cortex.doesNotExist", null, onComplete = {}, onError = { failed = it })

        val response = JSONObject()
            .put("id", frameId)
            .put("error", JSONObject().put("code", -32601).put("message", "method not found"))
        bridge.onMessage(response.toString())

        assertEquals("method not found", failed)
    }

    @Test
    fun `daemon notifications reach the listener`() {
        val listener = RecordingListener()
        val bridge = newBridge(listener)

        val notification = JSONObject()
            .put("jsonrpc", "2.0")
            .put("method", "cortex.event.taskCompleted")
            .put("params", JSONObject().put("taskId", "t-9").put("state", "completed"))
        bridge.onMessage(notification.toString())

        val (method, params) = listener.notifications.single()
        assertEquals("cortex.event.taskCompleted", method)
        assertEquals("completed", params.getString("state"))
    }

    @Test
    fun `enqueueSync matches the contract shape`() {
        val listener = RecordingListener()
        val bridge = newBridge(listener)
        val sent = mutableListOf<String>()
        bridge.sendOverride = { frame ->
            sent.add(frame)
            true
        }

        val entries = JSONArray().put(
            JSONObject()
                .put("id", "n1:create")
                .put("kind", "note")
                .put("action", "create")
                .put("payload", JSONObject().put("id", "n1")),
        )
        bridge.enqueueSync(entries, onComplete = {}, onError = {})

        val params = JSONObject(sent.single()).getJSONObject("params")
        assertEquals("syncNotes", params.getString("kind"))
        assertEquals("n1:create", params.getJSONArray("entries").getJSONObject(0).getString("id"))
    }

    @Test
    fun `unparseable and unknown messages are ignored`() {
        val listener = RecordingListener()
        val bridge = newBridge(listener)

        bridge.onMessage("not json at all")
        bridge.onMessage("""{"jsonrpc":"2.0","id":999,"result":{}}""") // no pending call with that id
        bridge.onMessage("""{"jsonrpc":"2.0"}""") // neither id nor method

        assertTrue(listener.notifications.isEmpty())
    }

    @Test
    fun `call while disconnected fails fast`() {
        val listener = RecordingListener()
        val bridge = CortexBridge(CortexBridge.DEFAULT_URL, listener) // NOT connected

        var failed: String? = null
        bridge.call("cortex.ping", null, onComplete = {}, onError = { failed = it })

        assertTrue(failed!!.contains("not connected"))
    }

    @Test
    fun `contract url matches the daemon endpoint`() {
        assertEquals("ws://127.0.0.1:9911/rpc", CortexBridge.DEFAULT_URL)
    }
}
