import { HttpClient } from '@angular/common/http';
import { Injectable } from '@angular/core';
import { BehaviorSubject, Subject, firstValueFrom } from 'rxjs';

/** GET /api/channel/:slug/admin/activity — where a channel stands (backend activity.go). */
export interface ChannelActivity {
  createdAt: string;
  messages: number;
  lastMessageAt: string | null;
  /** Signed-in readers who ever opened the channel. */
  subscribers: number;
  /** Devices subscribed to push notifications. */
  push: number;
  /** Writers and managers besides the owners. */
  team: number;
  owners: number;
  /** The owner turned the reminder mails off. */
  mailsOff: boolean;
  shareUrl: string;
}

export interface CelebrationAction {
  label: string;
  icon?: string;
  /** 'share' copies / shares the channel link; 'link' navigates to the route. */
  kind: 'share' | 'link';
  route?: string[];
}

export interface Celebration {
  title: string;
  text: string;
  emoji?: string;
  action?: CelebrationAction;
  /** The channel the celebration is about, for the share action. */
  slug?: string;
  channelName?: string;
}

/** Thresholds worth a "הידד!" — small at first, where it matters most. */
export const SUBSCRIBER_MILESTONES = [3, 10, 25, 50, 100, 250, 500, 1000, 2500, 5000, 10000, 25000, 50000, 100000];
export const MESSAGE_MILESTONES = [1, 10, 50, 100, 250, 500, 1000, 2500, 5000];

/** Days without a post after which the channel counts as quiet. */
export const QUIET_DAYS = 7;

interface SeenRecord {
  subs: number;
  msgs: number;
  team: number;
  /** Epoch ms until which the next-steps card stays folded away. */
  dismissedUntil?: number;
  /** The card content the manager marked "ראיתי"; it stays hidden until that changes. */
  seenKey?: string;
}

/** The highest milestone in `list` that `now` reached and `before` did not, or 0. */
export function crossedMilestone(list: number[], before: number, now: number): number {
  let hit = 0;
  for (const m of list) {
    if (before < m && now >= m) hit = m;
  }
  return hit;
}

export function daysSince(iso: string | null | undefined, now = Date.now()): number | null {
  if (!iso) return null;
  const t = Date.parse(iso);
  if (Number.isNaN(t)) return null;
  return Math.max(0, Math.floor((now - t) / 86_400_000));
}

/**
 * The channel's momentum: what the staff see about how the channel is doing,
 * and the "הידד!" moments when it grows. The celebrations go through one
 * stream so a single host (app.component) shows them on any screen — the
 * feed after a post, the team screen after a save, the manage page on a
 * milestone reached since the last visit.
 */
@Injectable({ providedIn: 'root' })
export class ActivityService {
  private celebrations = new Subject<Celebration>();
  readonly celebration$ = this.celebrations.asObservable();

  /** The latest activity per slug, so the feed card and the manage page agree. */
  private state = new BehaviorSubject<{ slug: string; activity: ChannelActivity } | undefined>(undefined);
  readonly activity$ = this.state.asObservable();

  constructor(private http: HttpClient) {}

  celebrate(c: Celebration): void {
    this.celebrations.next(c);
  }

  async load(slug: string): Promise<ChannelActivity> {
    const a = await firstValueFrom(this.http.get<ChannelActivity>(`/api/channel/${slug}/admin/activity`));
    this.state.next({ slug, activity: a });
    return a;
  }

  async setMailsOff(slug: string, off: boolean): Promise<void> {
    await firstValueFrom(this.http.post(`/api/channel/${slug}/admin/activity/mails`, { off }));
    const cur = this.state.value;
    if (cur?.slug === slug) this.state.next({ slug, activity: { ...cur.activity, mailsOff: off } });
  }

  /**
   * Reads the activity and cheers whatever grew since this browser last
   * looked. The first look only records: a channel opened on a new device
   * must not throw a party for a milestone reached a year ago.
   */
  async refreshAndCelebrate(slug: string, channelName: string): Promise<ChannelActivity | undefined> {
    let a: ChannelActivity;
    try {
      a = await this.load(slug);
    } catch {
      return undefined;
    }
    const seen = this.readSeen(slug);
    if (seen) {
      const c = this.milestoneFor(seen, a, slug, channelName);
      if (c) this.celebrate(c);
    }
    this.writeSeen(slug, { ...seen, subs: a.subscribers, msgs: a.messages, team: a.team });
    return a;
  }

  private milestoneFor(seen: SeenRecord, a: ChannelActivity, slug: string, channelName: string): Celebration | undefined {
    const base = { slug, channelName };
    const msg = crossedMilestone(MESSAGE_MILESTONES, seen.msgs, a.messages);
    if (msg === 1) {
      return {
        ...base, emoji: '🎉', title: 'הידד! ההודעה הראשונה פורסמה',
        text: 'הערוץ יצא לדרך. עכשיו הזמן לשתף את הקישור, כדי שהקוראים הראשונים יגיעו.',
        action: { kind: 'share', label: 'שיתוף הקישור לערוץ', icon: 'share-outline' },
      };
    }
    const subs = crossedMilestone(SUBSCRIBER_MILESTONES, seen.subs, a.subscribers);
    if (subs) {
      return {
        ...base, emoji: '🎊', title: `הידד! ${subs.toLocaleString('he-IL')} מנויים בערוץ`,
        text: subs < 100
          ? 'הקוראים מגיעים. עוד שיתוף אחד של הקישור יכול להביא את הבאים.'
          : 'קהל של ממש. המשיכו לפרסם בקביעות — זה מה ששומר עליהם.',
        action: { kind: 'share', label: 'שיתוף הקישור', icon: 'share-outline' },
      };
    }
    if (msg) {
      return {
        ...base, emoji: '✨', title: `הידד! ${msg.toLocaleString('he-IL')} הודעות בערוץ`,
        text: 'כל הכבוד על ההתמדה. ערוץ שמתעדכן בקביעות הוא ערוץ שחוזרים אליו.',
      };
    }
    if (a.team > seen.team && seen.team === 0) {
      return {
        ...base, emoji: '🙌', title: 'הידד! יש לכם שותף לערוץ',
        text: 'מעכשיו אפשר לפרסם ולנהל ביחד. ערוצים עם כמה כותבים מתעדכנים יותר.',
      };
    }
    return undefined;
  }

  /** Called by the composer after a post went out, so the first post is cheered at once. */
  afterPost(slug: string, channelName: string): void {
    void this.refreshAndCelebrate(slug, channelName);
  }

  isDismissed(slug: string): boolean {
    const until = this.readSeen(slug)?.dismissedUntil ?? 0;
    return until > Date.now();
  }

  dismiss(slug: string, days: number): void {
    const seen = this.readSeen(slug) ?? { subs: 0, msgs: 0, team: 0 };
    this.writeSeen(slug, { ...seen, dismissedUntil: Date.now() + days * 86_400_000 });
  }

  /** What the manager last marked as seen on the next-steps card, if anything. */
  seenKey(slug: string): string | undefined {
    return this.readSeen(slug)?.seenKey;
  }

  markSeen(slug: string, key: string): void {
    const seen = this.readSeen(slug) ?? { subs: 0, msgs: 0, team: 0 };
    this.writeSeen(slug, { ...seen, seenKey: key });
  }

  private key(slug: string): string {
    return `channel-momentum:${slug}`;
  }

  private readSeen(slug: string): SeenRecord | undefined {
    try {
      const raw = localStorage.getItem(this.key(slug));
      if (!raw) return undefined;
      const v = JSON.parse(raw);
      if (typeof v?.subs !== 'number' || typeof v?.msgs !== 'number') return undefined;
      return { subs: v.subs, msgs: v.msgs, team: typeof v.team === 'number' ? v.team : 0, dismissedUntil: v.dismissedUntil,
        seenKey: typeof v.seenKey === 'string' ? v.seenKey : undefined };
    } catch {
      return undefined;
    }
  }

  private writeSeen(slug: string, v: SeenRecord): void {
    try {
      localStorage.setItem(this.key(slug), JSON.stringify(v));
    } catch {
      /* private mode: the card just shows again next time */
    }
  }
}
