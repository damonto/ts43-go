/*
 * Standard TS.43 ODSA websheet callback adapter.
 *
 * Inject this into the carrier websheet shown by the example CLI. The adapter
 * provides the JavaScript bridge expected by TS.43 ODSA portals and prints a
 * JSON callback line that examples/websheet.go can parse directly.
 */
(function installODSAServiceFlow(global) {
  "use strict";

  const PREFIX = "TS43_ODSA_CALLBACK";
  const STORAGE_KEY = "ts43-go.odsa.callbacks";

  function emit(payload) {
    const record = Object.assign(
      {
        source: "odsa",
        href: global.location && global.location.href ? global.location.href : "",
        at: new Date().toISOString(),
      },
      payload,
    );
    const line = PREFIX + " " + JSON.stringify(record);
    append(record);
    if (global.console && typeof global.console.log === "function") {
      global.console.log(line);
    }
    try {
      global.dispatchEvent(new CustomEvent("ts43-odsa-callback", { detail: record }));
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

  function flow() {
    return {
      profileReadyWithActivationCode: function profileReadyWithActivationCode(
        activationCode,
        iccid,
        imei,
      ) {
        return emit({
          event: "profileReadyWithActivationCode",
          activationCode: activationCode || "",
          iccid: iccid || "",
          imei: imei || "",
        });
      },

      profileReadyWithDefaultSmdp: function profileReadyWithDefaultSmdp(
        defaultSmdpAddress,
        iccid,
        imei,
      ) {
        return emit({
          event: "profileReadyWithDefaultSmdp",
          defaultSmdpAddress: defaultSmdpAddress || "",
          iccid: iccid || "",
          imei: imei || "",
        });
      },

      selectionCompleted: function selectionCompleted(iccid, imei) {
        return emit({
          event: "selectionCompleted",
          iccid: iccid || "",
          imei: imei || "",
        });
      },

      finishFlow: function finishFlow(nextAction) {
        return emit({
          event: "finishFlow",
          nextAction: nextAction || "",
        });
      },

      dismissFlow: function dismissFlow() {
        return emit({ event: "dismissFlow" });
      },

      deleteToken: function deleteToken() {
        return emit({ event: "deleteToken" });
      },

      checkProfileServiceStatus: function checkProfileServiceStatus() {
        return emit({ event: "checkProfileServiceStatus" });
      },

      deleteProfileInUse: function deleteProfileInUse(iccid) {
        return emit({
          event: "deleteProfileInUse",
          iccid: iccid || "",
        });
      },
    };
  }

  global.ODSAServiceFlow = Object.assign(flow(), global.ODSAServiceFlow || {});
  global.ts43ODSAServiceFlow = Object.freeze({
    callbacks: function callbacks() {
      try {
        return JSON.parse(global.localStorage.getItem(STORAGE_KEY) || "[]");
      } catch (_) {
        return [];
      }
    },
  });

  if (global.console && typeof global.console.info === "function") {
    global.console.info("TS.43 ODSAServiceFlow adapter installed.");
  }
})(window);
