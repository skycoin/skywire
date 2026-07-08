// voice-audio.js — main-thread WebAudio proxy for the wasm-visor's 1:1 voice.
//
// getUserMedia + AudioContext are main-thread-only, but the visor's audio
// Source/Sink live in the wasm (pkg/skychat/voice/audio_wasm.go) and are driven
// through three globals the wasm registers: __skyvoiceMic(Uint8Array of LE int16),
// __skyvoicePlay(nSamples)->Uint8Array, __skyvoiceActive()->bool. This file wires
// a WebAudio graph to those: it captures the mic and hands frames to __skyvoiceMic,
// and pulls playback frames from __skyvoicePlay into the speakers.
//
// This works when the wasm runs in the SAME context as this script (the in-page /
// single-file visor), so the globals are directly callable. When the visor runs
// in a Web Worker, these globals live in the worker; a SharedArrayBuffer ring
// (needs COOP/COEP) or a message bridge is required to reach them — a follow-up.
//
// PCM is 48 kHz mono int16 to match pkg/skychat/voice.
(function () {
  var RATE = 48000;
  var ctx = null, micStream = null, capNode = null, playNode = null, src = null;

  function have(fn) { return typeof globalThis[fn] === 'function'; }

  // start requests the mic and connects capture + playback graphs.
  async function start() {
    if (ctx) return; // already running
    if (!have('__skyvoiceMic') || !have('__skyvoicePlay')) {
      throw new Error('voice audio bridge not present (worker-mode needs a SAB bridge)');
    }
    ctx = new (window.AudioContext || window.webkitAudioContext)({ sampleRate: RATE });
    if (ctx.state === 'suspended') await ctx.resume();

    // --- capture: mic -> __skyvoiceMic ---
    micStream = await navigator.mediaDevices.getUserMedia({ audio: { channelCount: 1, echoCancellation: true, noiseSuppression: true } });
    src = ctx.createMediaStreamSource(micStream);
    capNode = ctx.createScriptProcessor(2048, 1, 1);
    capNode.onaudioprocess = function (e) {
      var f32 = e.inputBuffer.getChannelData(0);
      var i16 = new Int16Array(f32.length);
      for (var i = 0; i < f32.length; i++) {
        var s = Math.max(-1, Math.min(1, f32[i]));
        i16[i] = s < 0 ? s * 0x8000 : s * 0x7fff;
      }
      globalThis.__skyvoiceMic(new Uint8Array(i16.buffer));
    };
    src.connect(capNode);
    capNode.connect(ctx.destination); // required for the node to run (output is silent)

    // --- playback: __skyvoicePlay -> speakers ---
    playNode = ctx.createScriptProcessor(2048, 1, 1);
    playNode.onaudioprocess = function (e) {
      var out = e.outputBuffer.getChannelData(0);
      var bytes = globalThis.__skyvoicePlay(out.length); // Uint8Array of LE int16
      var i16 = new Int16Array(bytes.buffer, bytes.byteOffset, out.length);
      for (var i = 0; i < out.length; i++) out[i] = i16[i] / 0x8000;
    };
    playNode.connect(ctx.destination);
  }

  // stop tears the graph down and releases the mic.
  function stop() {
    try { if (capNode) capNode.disconnect(); } catch (_) {}
    try { if (playNode) playNode.disconnect(); } catch (_) {}
    try { if (src) src.disconnect(); } catch (_) {}
    try { if (micStream) micStream.getTracks().forEach(function (t) { t.stop(); }); } catch (_) {}
    try { if (ctx) ctx.close(); } catch (_) {}
    ctx = micStream = capNode = playNode = src = null;
  }

  globalThis.skywireVoiceAudioStart = start;
  globalThis.skywireVoiceAudioStop = stop;
})();
