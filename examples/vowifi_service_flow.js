/*
 * Standard TS.43 VoWiFi web portal callback adapter.
 *
 * Supports TS.43 callbacks and the Google Wi-Fi Calling portal bridge:
 * - VoWiFiWebServiceFlow.entitlementChanged()
 * - VoWiFiWebServiceFlow.dismissFlow()
 */
(function installVoWiFiServiceFlow(global) {
  "use strict";

  const PREFIX = "TS43_VOWIFI_CALLBACK";
  const STORAGE_KEY = "ts43-go.vowifi.callbacks";

  function emit(event) {
    const record = {
      source: "vowifi",
      event,
      href: global.location && global.location.href ? global.location.href : "",
      at: new Date().toISOString(),
    };
    const line = PREFIX + " " + JSON.stringify(record);
    append(record);
    if (global.console && typeof global.console.log === "function") {
      global.console.log(line);
    }
    try {
      global.dispatchEvent(new CustomEvent("ts43-vowifi-callback", { detail: record }));
    } catch (_) {
      // Older embedded WebViews may not expose CustomEvent.
    }
    return record;
  }

  function append(record) {
    try {
      const existing = JSON.parse(global.localStorage.getItem(STORAGE_KEY) || "[]");
      existing.push(record);
      global.localStorage.setItem(STORAGE_KEY, JSON.stringify(existing.slice(-20)));
    } catch (_) {
      // Some carrier pages block storage. Console output is the primary path.
    }
  }

  function install(name) {
    const callbacks = {
      entitlementChanged: () => emit("entitlementChanged"),
      CloseWebView: () => emit("entitlementChanged"),
      dismissFlow: () => emit("dismissFlow"),
      cancelButtonClicked: () => emit("dismissFlow"),
      cancelButtonPressed: () => emit("dismissFlow"),
      phoneServicesAccountStatusChanged: () => emit("entitlementChanged"),
      workflowAbandoned: () => emit("entitlementChanged"),
      workflowCompleted: () => emit("entitlementChanged"),
    };
    global[name] = Object.assign(callbacks, global[name] || {});
    return global[name];
  }

  // Set this to WFCConfig.WebsheetClientName before injecting the adapter.
  const clientName = global.TS43_WFC_CLIENT_NAME || "WiFiCallingWebViewController";
  install(clientName);
  install("VoWiFiWebServiceFlow");
  install("WiFiCallingWebViewController");

  global.ts43VoWiFiServiceFlow = Object.freeze({
    install,
    done: global[clientName].entitlementChanged,
    dismiss: global[clientName].dismissFlow,
    callbacks: function callbacks() {
      try {
        return JSON.parse(global.localStorage.getItem(STORAGE_KEY) || "[]");
      } catch (_) {
        return [];
      }
    },
  });

  if (global.console && typeof global.console.info === "function") {
    global.console.info("TS.43 VoWiFiWebServiceFlow adapter installed.");
  }
})(window);
