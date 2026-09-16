// Copyright 2026 TPT Solutions. Dual-licensed MIT OR Apache-2.0.
package solutions.tpt.cortex

import android.content.Intent
import android.net.Uri
import android.os.Bundle
import android.webkit.WebView
import android.webkit.WebViewClient
import androidx.appcompat.app.AppCompatActivity

/**
 * Thin WebView host. Distribution strategy (spec §7): the user installs the
 * PWA from the browser; this app exists to run the daemon and to hand out
 * native capabilities over the loopback contract. It loads the PWA origin
 * (configurable) and handles tpt:// deep links.
 */
class MainActivity : AppCompatActivity() {

    private lateinit var webView: WebView

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        webView = WebView(this)
        webView.settings.javaScriptEnabled = true
        webView.settings.domStorageEnabled = true
        webView.webViewClient = WebViewClient()
        setContentView(webView)

        DaemonService.start(this)

        val deepLink = intent?.data
        if (deepLink != null) {
            handleDeepLink(deepLink)
        } else {
            webView.loadUrl(DEFAULT_PWA_URL)
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

    companion object {
        // The deployed PWA origin; the WebView keeps its own storage, so this
        // shares state with the browser-installed PWA only via the daemon.
        const val DEFAULT_PWA_URL = "https://pwa.tpt.example/"
    }
}
