import { useCallback, useEffect, useRef, useState, type RefObject } from "react";
import { api } from "./api";
import { i18n, useLanguage } from "./i18n";

type DictationProps = {
  value: string;
  textareaRef: RefObject<HTMLTextAreaElement | null>;
  disabled: boolean;
  onChange: (value: string) => void;
};

type Recording = {
  recorder: MediaRecorder;
  stream: MediaStream;
  chunks: Blob[];
  token: number;
  cancelled: boolean;
};

type DesktopBridge = {
  requestMicrophoneAccess?: () => Promise<boolean>;
};

function insertDictation(prefix: string, suffix: string, transcript: string) {
  const text = transcript.trim();
  if (!text) return `${prefix}${suffix}`;
  const before = prefix && !/\s$/.test(prefix) ? " " : "";
  const after = suffix && !/^\s/.test(suffix) ? " " : "";
  return `${prefix}${before}${text}${after}${suffix}`;
}

function browserSupport() {
  if (!window.isSecureContext) return { supported: false, notice: i18n.t("settings:dictation.needs_https") };
  if (!navigator.mediaDevices?.getUserMedia || typeof MediaRecorder === "undefined") return { supported: false, notice: i18n.t("settings:dictation.unsupported_browser") };
  return { supported: true, notice: "" };
}

function recordingMimeType() {
  const candidates = ["audio/mp4", "audio/webm;codecs=opus", "audio/webm"];
  return candidates.find((value) => MediaRecorder.isTypeSupported(value)) ?? "";
}

export function useDictation({ value, textareaRef, disabled, onChange }: DictationProps) {
  const [starting, setStarting] = useState(false);
  const [listening, setListening] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const [permissionNeeded, setPermissionNeeded] = useState(false);
  const [supported, setSupported] = useState(false);
  const [elapsed, setElapsed] = useState(0);
  const [levels, setLevels] = useState<number[]>(() => Array(36).fill(4));
  const recordingRef = useRef<Recording | null>(null);
  const operationRef = useRef(0);
  const startingRef = useRef(false);
  const prefixRef = useRef("");
  const suffixRef = useRef("");
  const abortRef = useRef<AbortController | null>(null);
  const meterRef = useRef<{ context: AudioContext; frame: number } | null>(null);
  const timerRef = useRef<number | null>(null);

  const stopMeter = useCallback(() => {
    if (timerRef.current !== null) window.clearInterval(timerRef.current);
    timerRef.current = null;
    const meter = meterRef.current;
    if (!meter) return;
    cancelAnimationFrame(meter.frame);
    void meter.context.close().catch(() => undefined);
    meterRef.current = null;
  }, []);

  // Re-read on a language switch so the notice follows the UI language.
  const { language } = useLanguage();
  useEffect(() => {
    const support = browserSupport();
    setSupported(support.supported);
    setNotice(support.notice);
  }, [language]);

  const finish = useCallback(async (recording: Recording) => {
    if (recording.cancelled || recordingRef.current?.token !== recording.token) return;
    stopMeter();
    recording.stream.getTracks().forEach((track) => track.stop());
    recordingRef.current = null;
    setListening(false);
    setBusy(true);
    setError("");
    setPermissionNeeded(false);
    const controller = new AbortController();
    abortRef.current = controller;
    try {
      const blob = new Blob(recording.chunks, { type: recording.recorder.mimeType || "audio/mp4" });
      if (blob.size === 0) throw new Error(i18n.t("settings:dictation.no_audio"));
      const result = await api.transcribeDictation(blob, controller.signal);
      if (!controller.signal.aborted && result.text.trim()) onChange(insertDictation(prefixRef.current, suffixRef.current, result.text));
    } catch (cause) {
      if (!controller.signal.aborted) setError(cause instanceof Error ? cause.message : i18n.t("settings:dictation.failed"));
    } finally {
      if (abortRef.current === controller) abortRef.current = null;
      if (!controller.signal.aborted) setBusy(false);
    }
  }, [onChange, stopMeter]);

  const stop = useCallback(() => {
    const recording = recordingRef.current;
    if (!recording || recording.recorder.state === "inactive") return;
    recording.recorder.stop();
  }, []);

  const cancel = useCallback(() => {
    operationRef.current += 1;
    startingRef.current = false;
    setStarting(false);
    const recording = recordingRef.current;
    if (recording) {
      recording.cancelled = true;
      recordingRef.current = null;
      if (recording.recorder.state !== "inactive") recording.recorder.stop();
      recording.stream.getTracks().forEach((track) => track.stop());
      stopMeter();
      setListening(false);
    }
    abortRef.current?.abort();
    abortRef.current = null;
    setBusy(false);
    setElapsed(0);
    setError("");
  }, [stopMeter]);

  const start = useCallback(async () => {
    if (disabled || listening || busy || startingRef.current) return;
    if (!supported) { setError(notice || i18n.t("settings:dictation.unavailable")); return; }
    const token = ++operationRef.current;
    startingRef.current = true;
    setStarting(true);
    const cursor = textareaRef.current?.selectionStart ?? value.length;
    prefixRef.current = value.slice(0, cursor);
    suffixRef.current = value.slice(cursor);
    setError("");
    try {
      // Let the connecting state paint before browser permission and recorder
      // setup can occupy the main thread.
      await new Promise<void>((resolve) => requestAnimationFrame(() => window.setTimeout(resolve, 0)));
      if (operationRef.current !== token) return;
      const desktop = (window as Window & { tofiDesktop?: DesktopBridge }).tofiDesktop;
      if (desktop?.requestMicrophoneAccess && !(await desktop.requestMicrophoneAccess())) {
        throw new DOMException("Microphone permission was denied.", "NotAllowedError");
      }
      if (operationRef.current !== token) return;
      const stream = await navigator.mediaDevices.getUserMedia({ audio: true });
      if (operationRef.current !== token) { stream.getTracks().forEach((track) => track.stop()); return; }
      const recorder = new MediaRecorder(stream, (() => { const mimeType = recordingMimeType(); return mimeType ? { mimeType } : undefined; })());
      const recording: Recording = { recorder, stream, chunks: [], token, cancelled: false };
      recorder.ondataavailable = (event) => { if (event.data.size > 0) recording.chunks.push(event.data); };
      recorder.onerror = () => { stopMeter(); stream.getTracks().forEach((track) => track.stop()); recordingRef.current = null; setListening(false); setError(i18n.t("settings:dictation.recording_failed")); };
      recorder.onstop = () => { void finish(recording); };
      recordingRef.current = recording;
      recorder.start();
      const startedAt = performance.now();
      setElapsed(0);
      setLevels(Array(36).fill(4));
      timerRef.current = window.setInterval(() => {
        if (recordingRef.current?.token === recording.token) setElapsed(Math.max(0, Math.floor((performance.now() - startedAt) / 1000)));
      }, 100);
      setListening(true);
      requestAnimationFrame(() => {
        if (recordingRef.current?.token !== token) return;
        try {
          const context = new AudioContext();
          const analyser = context.createAnalyser();
          analyser.fftSize = 1024;
          context.createMediaStreamSource(stream).connect(analyser);
          const samples = new Uint8Array(analyser.fftSize);
          const meter = { context, frame: 0 };
          meterRef.current = meter;
          let lastSample = 0;
          const sample = (now: number) => {
            if (now - lastSample >= 65) {
              analyser.getByteTimeDomainData(samples);
              let energy = 0;
              for (const value of samples) energy += ((value - 128) / 128) ** 2;
              const height = Math.max(4, Math.min(36, Math.round(Math.sqrt(energy / samples.length) * 125)));
              setLevels((current) => [...current.slice(1), height]);
              lastSample = now;
            }
            meter.frame = requestAnimationFrame(sample);
          };
          meter.frame = requestAnimationFrame(sample);
        } catch { /* Recording still works if the browser cannot provide an audio meter. */ }
      });
    } catch (cause) {
      if (operationRef.current !== token) return;
      const failedRecording = recordingRef.current;
      if (failedRecording) {
        failedRecording.cancelled = true;
        failedRecording.stream.getTracks().forEach((track) => track.stop());
        recordingRef.current = null;
      }
      stopMeter();
      const permissionError = cause instanceof DOMException && (cause.name === "NotAllowedError" || cause.name === "SecurityError");
      setPermissionNeeded(permissionError);
      setError(permissionError ? i18n.t("settings:dictation.permission_needed") : i18n.t("settings:dictation.start_failed"));
    } finally {
      if (operationRef.current === token) { startingRef.current = false; setStarting(false); }
    }
  }, [busy, disabled, finish, listening, notice, stopMeter, supported, textareaRef, value]);

  useEffect(() => () => {
    operationRef.current += 1;
    abortRef.current?.abort();
    stopMeter();
    const recording = recordingRef.current;
    recording?.stream.getTracks().forEach((track) => track.stop());
    recordingRef.current = null;
  }, [stopMeter]);

  return { starting, listening, busy, elapsed, levels, error, notice, supported, permissionNeeded, clearPermission: () => setPermissionNeeded(false), start, confirm: stop, cancel };
}
