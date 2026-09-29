// Copyright 2026 TPT Solutions. Dual-licensed MIT OR Apache-2.0.
package solutions.tpt.cortex

import android.content.ComponentName
import android.content.Context
import android.content.Intent
import android.content.ServiceConnection
import android.net.Uri
import android.os.Bundle
import android.os.IBinder
import android.webkit.WebView
import android.webkit.WebViewClient
import androidx.appcompat.app.AppCompatActivity
import org.json.JSONObject

/**
 * Thin WebView host. Distribution strategy (spec §7): the user installs the
 * PWA from the browser; this app exists to run the daemon and to hand out
 * native capabilities over the loopback contract. It loads the PWA origin
 * (configurable) and handles tpt:// deep links.
 *
 * The PWA talks to the daemon DIRECTLY over ws://127.0.0.1:9911 (permitted
 * by the loopback network security config). This activity additionally
 * relays daemon notifications arriving via the service's bridge into the
 * WebView as `cortex:notification` DOM events, covering reconnect windows.
 */
class MainActivity : AppCompatActivity() {

    private lateinit var webView: WebView
    private var daemon: DaemonService.LocalBinder? = null
    private val notificationRelay: (String, JSONObject) -> Unit = { method, _ ->
        runOnUiThread {
            val script = "window.dispatchEvent(new CustomEvent('cortex:notification',{detail:{method:'$method'}}));"
            webView.evaluateJavascript(script, null)
        }
    }
    private val stateRelay: (CortexBridge.State) -> Unit = { state ->
        runOnUiThread {
            val script = "window.dispatchEvent(new CustomEvent('cortex:state',{detail:{state:'$state'}}));"
            webView.evaluateJavascript(script, null)
        }
    }
    // The embedded daemon requires a shared token on /rpc; the PWA inside the
    // WebView needs it for its own direct WebSocket, so it arrives as a
    // cortex:auth DOM event (and again after every page load, since page
    // loads reset the page's JS state).
    @Volatile private var lastDaemonToken: String? = null
    private val authRelay: (String) -> Unit = { token ->
        lastDaemonToken = token
        dispatchAuth(token)
    }

    private fun dispatchAuth(token: String) {
        runOnUiThread {
            val script = "window.dispatchEvent(new CustomEvent('cortex:auth',{detail:{token:'$token'}}));"
            webView.evaluateJavascript(script, null)
        }
    }

    private val serviceConnection = object : ServiceConnection {
        override fun onServiceConnected(name: ComponentName?, binder: IBinder?) {
            daemon = binder as? DaemonService.LocalBinder
            daemon?.addNotificationListener(notificationRelay)
            daemon?.addStateListener(stateRelay)
            daemon?.addAuthListener(authRelay)
        }

        override fun onServiceDisconnected(name: ComponentName?) {
            daemon = null
        }
    }

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        webView = WebView(this)
        webView.settings.javaScriptEnabled = true
        webView.settings.domStorageEnabled = true
        webView.webViewClient = WebViewClient()
        setContentView(webView)

        DaemonService.start(this)
        bindService(Intent(this, DaemonService::class.java), serviceConnection, Context.BIND_AUTO_CREATE)

        val deepLink = intent?.data
        if (deepLink != null) {
            handleDeepLink(deepLink)
        } else {
            webView.loadUrl(DEFAULT_PWA_URL)
        }
        // Re-hand the daemon token whenever the page (re)loads: page loads
        // reset the PWA's JS state, losing the earlier cortex:auth event.
        webView.webViewClient = object : WebViewClient() {
            override fun onPageFinished(view: WebView?, url: String?) {
                lastDaemonToken?.let { dispatchAuth(it) }
            }
        }
    }

    override fun onNewIntent(intent: Intent) {
        super.onNewIntent(intent)
        intent.data?.let { handleDeepLink(it) }
    }

    private fun handleDeepLink(uri: Uri) {
        // tpt://open, tpt://settings, ... are routed into the PWA shell for now.
        val route = uri.host ?: return
        webView.loadUrl("$DEFAULT_PWA_URL#$route")
    }

    override fun onBackPressed() {
        if (webView.canGoBack()) webView.goBack() else super.onBackPressed()
    }

    override fun onDestroy() {
        daemon?.removeNotificationListener(notificationRelay)
        daemon?.removeStateListener(stateRelay)
        daemon?.removeAuthListener(authRelay)
        unbindService(serviceConnection)
        super.onDestroy()
    }

    companion object {
        // The deployed PWA origin; the WebView keeps its own storage, so this
        // shares state with the browser-installed PWA only via the daemon.
        const val DEFAULT_PWA_URL = "https://pwa.tpt.example/"
    }
}
