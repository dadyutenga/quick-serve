(function () {
  const TOKEN_KEY = "__quick_token";

  function getToken() {
    // token is injected by the server at serve-time via window.__QUICK_TOKEN__
    return window.__QUICK_TOKEN__ || window[TOKEN_KEY] || "";
  }

  function apiBase() {
    // In path-based dev mode (/s/name/...), prefix API with /s/name
    var m = location.pathname.match(/^\/s\/([^/]+)/);
    if (m) return "/s/" + m[1];
    return "";
  }

  async function request(path, opts) {
    opts = opts || {};
    var headers = Object.assign(
      {
        "Content-Type": "application/json",
        "X-Quick-Token": getToken(),
      },
      opts.headers || {}
    );
    // Don't force JSON content-type when body is FormData
    if (opts.body && typeof FormData !== "undefined" && opts.body instanceof FormData) {
      delete headers["Content-Type"];
    }
    var res = await fetch(apiBase() + path, Object.assign({}, opts, { headers: headers }));
    if (!res.ok) {
      var err = await res.json().catch(function () {
        return { error: res.statusText };
      });
      throw new Error(err.error || "Request failed: " + res.status);
    }
    if (res.status === 204) return null;
    return res.json();
  }

  var quick = {
    data: {
      set: function (key, value) {
        return request("/api/data/" + encodeURIComponent(key), {
          method: "POST",
          body: JSON.stringify(value),
        });
      },
      get: function (key) {
        return request("/api/data/" + encodeURIComponent(key));
      },
      delete: function (key) {
        return request("/api/data/" + encodeURIComponent(key), { method: "DELETE" });
      },
      list: function () {
        return request("/api/data");
      },
    },

    files: {
      upload: async function (file) {
        var form = new FormData();
        form.append("file", file);
        var res = await fetch(apiBase() + "/api/files", {
          method: "POST",
          headers: { "X-Quick-Token": getToken() },
          body: form,
        });
        if (!res.ok) {
          var err = await res.json().catch(function () {
            return { error: "Upload failed" };
          });
          throw new Error(err.error || "Upload failed");
        }
        return res.json();
      },
      url: function (filename) {
        return apiBase() + "/api/files/" + encodeURIComponent(filename);
      },
      list: function () {
        return request("/api/files");
      },
      delete: function (filename) {
        return request("/api/files/" + encodeURIComponent(filename), {
          method: "DELETE",
        });
      },
    },

    ai: function (prompt, opts) {
      opts = opts || {};
      return request("/api/ai", {
        method: "POST",
        body: JSON.stringify(Object.assign({ prompt: prompt }, opts)),
      }).then(function (r) {
        return r.text;
      });
    },

    ws: (function () {
      var socket = null;
      var handlers = {};
      return {
        connect: function (room) {
          var proto = location.protocol === "https:" ? "wss" : "ws";
          var base = apiBase();
          var host = location.host;
          var token = encodeURIComponent(getToken());
          var q =
            "room=" +
            encodeURIComponent(room || "default") +
            "&token=" +
            token;
          // Path-based (/s/name/api/ws) and subdomain (/api/ws) both supported server-side.
          // Token in query is required for browser WS (cannot set headers on upgrade);
          // site tokens are public-tier credentials by design (embedded in HTML).
          var path = base ? base + "/api/ws" : "/api/ws";
          socket = new WebSocket(proto + "://" + host + path + "?" + q);
          socket.onmessage = function (evt) {
            try {
              var msg = JSON.parse(evt.data);
              var list = handlers[msg.type] || [];
              for (var i = 0; i < list.length; i++) {
                list[i](msg.data, msg);
              }
            } catch (e) {
              /* ignore */
            }
          };
          return new Promise(function (resolve, reject) {
            socket.onopen = function () {
              resolve();
            };
            socket.onerror = function (err) {
              reject(err);
            };
          });
        },
        on: function (type, fn) {
          (handlers[type] = handlers[type] || []).push(fn);
        },
        send: function (type, data) {
          if (socket && socket.readyState === 1) {
            socket.send(JSON.stringify({ type: type, data: data }));
          }
        },
        close: function () {
          if (socket) socket.close();
        },
      };
    })(),
  };

  window.quick = quick;
})();
