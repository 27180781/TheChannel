import { Injectable } from '@angular/core';
import { ChatMessage } from './chat.service';

export interface MagnetSettings {
  enabled: boolean;
  snippet: string;
  mode: 'by_messages' | 'by_time' | string;
  perMessages: number;
  minTimeSeconds: number;
  perSeconds: number;
  minMessagesSinceLast: number;
}

export type ChatItem =
  | { kind: 'message'; message: ChatMessage; trackKey: string }
  | { kind: 'ad'; trackKey: string };

@Injectable({ providedIn: 'root' })
export class MagnetAdsService {
  private settings: MagnetSettings | null = null;
  private settingsPromise: Promise<MagnetSettings | null> | null = null;

  loadSettings(force = false): Promise<MagnetSettings | null> {
    if (this.settings && !force) return Promise.resolve(this.settings);
    if (this.settingsPromise && !force) return this.settingsPromise;

    this.settingsPromise = fetch('/api/ads/magnet')
      .then(r => r.ok ? r.json() : null)
      .then((data: MagnetSettings | null) => {
        this.settings = data;
        return data;
      })
      .catch(() => null);

    return this.settingsPromise;
  }

  getSettings(): MagnetSettings | null {
    return this.settings;
  }

  /**
   * Build a list of chat items (messages + ad slots) for the chat component.
   * Input: messages array as held in chat.component (newest at index 0).
   * Output: items in the same order (newest first), with ad slots interleaved
   * according to the configured rules. The list is rendered by a flex-column-reverse
   * container, so ad slots appear visually between messages in chronological order.
   */
  buildItems(messages: ChatMessage[]): ChatItem[] {
    if (!messages || messages.length === 0) return [];

    const s = this.settings;
    if (!s || !s.enabled || !s.snippet?.trim()) {
      return messages.map(m => ({
        kind: 'message',
        message: m,
        trackKey: `m-${m.id}`,
      } as ChatItem));
    }

    const chrono = [...messages].reverse();
    const out: ChatItem[] = [];

    if (s.mode === 'by_time') {
      out.push(...this.buildByTime(chrono, s));
    } else {
      out.push(...this.buildByMessages(chrono, s));
    }

    return out.reverse();
  }

  private buildByMessages(chrono: ChatMessage[], s: MagnetSettings): ChatItem[] {
    const per = Math.max(1, s.perMessages || 5);
    const minTime = Math.max(0, s.minTimeSeconds || 0);
    const out: ChatItem[] = [];

    let countSinceLast = 0;
    let lastAdTime: number | null = null;

    for (const m of chrono) {
      out.push({ kind: 'message', message: m, trackKey: `m-${m.id}` });
      countSinceLast++;

      if (countSinceLast >= per) {
        const msgTime = this.toEpoch(m.timestamp);
        const enoughTimePassed = !minTime || lastAdTime === null ||
          (msgTime !== null && (msgTime - lastAdTime) >= minTime * 1000);

        if (enoughTimePassed) {
          out.push({ kind: 'ad', trackKey: `ad-after-${m.id}` });
          countSinceLast = 0;
          if (msgTime !== null) lastAdTime = msgTime;
        }
      }
    }

    return out;
  }

  private buildByTime(chrono: ChatMessage[], s: MagnetSettings): ChatItem[] {
    const per = Math.max(1, s.perSeconds || 60);
    const minMsgs = Math.max(0, s.minMessagesSinceLast || 0);
    const out: ChatItem[] = [];

    let lastAdTime: number | null = null;
    let msgsSinceLast = 0;

    for (const m of chrono) {
      out.push({ kind: 'message', message: m, trackKey: `m-${m.id}` });
      msgsSinceLast++;

      const msgTime = this.toEpoch(m.timestamp);
      if (msgTime === null) continue;

      if (lastAdTime === null) {
        lastAdTime = msgTime;
        continue;
      }

      const elapsed = (msgTime - lastAdTime) / 1000;
      if (elapsed >= per && msgsSinceLast >= minMsgs) {
        out.push({ kind: 'ad', trackKey: `ad-after-${m.id}` });
        lastAdTime = msgTime;
        msgsSinceLast = 0;
      }
    }

    return out;
  }

  private toEpoch(ts: any): number | null {
    if (!ts) return null;
    if (ts instanceof Date) return ts.getTime();
    const t = new Date(ts).getTime();
    return isNaN(t) ? null : t;
  }
}
