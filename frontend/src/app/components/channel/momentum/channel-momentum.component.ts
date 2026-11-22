import { Component, Input, OnChanges, OnDestroy, SimpleChanges } from '@angular/core';
import { NgTemplateOutlet } from '@angular/common';
import { RouterLink } from '@angular/router';
import { NbButtonModule, NbIconModule, NbToastrService, NbToggleModule } from '@nebular/theme';
import { ActivityService, ChannelActivity, QUIET_DAYS, daysSince } from '../../../services/activity.service';
import { ShareService } from '../../../services/share.service';
import { ChatService } from '../../../services/chat.service';

export interface MomentumStep {
  id: 'post' | 'profile' | 'share' | 'team';
  title: string;
  hint: string;
  done: boolean;
}

/** Readers that make "the link was shared" true, besides the owners themselves. */
const FIRST_READERS = 3;

/**
 * Where the channel stands and what to do next, for its managers.
 *
 *  - `feed`: a small card pinned under the channel header. It names the next
 *    step (or how long the channel has been quiet), opens into the full list,
 *    and can be put away for a few days ("לא עכשיו") or marked "ראיתי",
 *    which hides it until the next step changes. It disappears once every step is
 *    done and the channel is posting.
 *  - `panel`: the same steps at the top of the manage page, with the numbers
 *    and the owner's switch for the reminder mails.
 *
 * Every load also checks for milestones reached since this browser last
 * looked (ActivityService.refreshAndCelebrate), which is where most of the
 * "הידד!" moments come from.
 */
@Component({
  selector: 'app-channel-momentum',
  standalone: true,
  imports: [NgTemplateOutlet, RouterLink, NbButtonModule, NbIconModule, NbToggleModule],
  templateUrl: './channel-momentum.component.html',
  styleUrl: './channel-momentum.component.scss',
})
export class ChannelMomentumComponent implements OnChanges, OnDestroy {
  @Input() slug = '';
  @Input() mode: 'feed' | 'panel' = 'feed';
  /** Owner (or super admin): may add staff and switch the reminder mails. */
  @Input() isOwner = false;

  activity?: ChannelActivity;
  steps: MomentumStep[] = [];
  open = false;
  hidden = false;
  savingMails = false;

  private refreshTimer?: ReturnType<typeof setInterval>;

  constructor(
    private activityService: ActivityService,
    private share: ShareService,
    private chat: ChatService,
    private toastr: NbToastrService,
  ) {}

  ngOnChanges(changes: SimpleChanges): void {
    if (changes['slug'] && this.slug) {
      this.hidden = this.mode === 'feed' && this.activityService.isDismissed(this.slug);
      this.seenKey = this.activityService.seenKey(this.slug);
      void this.reload();
      clearInterval(this.refreshTimer);
      // New readers arrive while the owner sits on the feed; a slow poll is
      // enough to cheer them within minutes.
      this.refreshTimer = setInterval(() => void this.reload(), 5 * 60_000);
    }
  }

  ngOnDestroy(): void {
    clearInterval(this.refreshTimer);
  }

  get channelName(): string {
    return this.chat.channelInfo?.name || this.slug;
  }

  get doneCount(): number {
    return this.steps.filter(s => s.done).length;
  }

  get nextStep(): MomentumStep | undefined {
    return this.steps.find(s => !s.done);
  }

  get quietDays(): number | null {
    const a = this.activity;
    if (!a || !a.messages) return null;
    const d = daysSince(a.lastMessageAt);
    return d !== null && d >= QUIET_DAYS ? d : null;
  }

  get lastPostLabel(): string {
    const d = daysSince(this.activity?.lastMessageAt);
    if (d === null) return 'עדיין לא';
    if (d === 0) return 'היום';
    if (d === 1) return 'אתמול';
    return `לפני ${d} ימים`;
  }

  get progress(): number {
    return this.steps.length ? Math.round((this.doneCount / this.steps.length) * 100) : 0;
  }

  /** The feed card shows only while there is something to suggest. */
  get showInFeed(): boolean {
    if (this.hidden || !this.activity) return false;
    // An empty channel already has its own "הערוץ מוכן!" invitation in the feed.
    if (this.activity.messages === 0) return false;
    const key = this.cardKey;
    return !!key && key !== this.seenKey;
  }

  /**
   * What the collapsed card is about right now. "ראיתי" remembers it, so the
   * card comes back only when there is something new: the next step changed,
   * or the channel went quiet again after a newer post.
   */
  get cardKey(): string | undefined {
    if (this.quietDays !== null) return `quiet:${this.activity?.lastMessageAt ?? ''}`;
    return this.nextStep ? `step:${this.nextStep.id}` : undefined;
  }

  seenKey?: string;

  async reload(): Promise<void> {
    const slug = this.slug;
    const a = await this.activityService.refreshAndCelebrate(slug, this.channelName);
    if (!a || slug !== this.slug) return;
    this.activity = a;
    this.steps = this.buildSteps(a);
  }

  private buildSteps(a: ChannelActivity): MomentumStep[] {
    const info = this.chat.channelInfo;
    return [
      {
        id: 'post', title: 'פרסום ההודעה הראשונה', done: a.messages > 0,
        hint: 'הקוראים הראשונים צריכים משהו לקרוא כשהם מגיעים.',
      },
      {
        id: 'profile', title: 'לוגו ותיאור לערוץ', done: !!info?.logoUrl && !!info?.description?.trim(),
        hint: 'ערוץ עם תמונה ומשפט הסבר מקבל יותר הצטרפויות.',
      },
      {
        id: 'share', title: `שיתוף הקישור ו-${FIRST_READERS} מנויים ראשונים`,
        done: a.subscribers - a.owners >= FIRST_READERS,
        hint: 'שלחו את הקישור בוואטסאפ, במייל או בקבוצה — כל מי שנכנס נספר כאן.',
      },
      {
        id: 'team', title: 'הוספת כותב או מנהל', done: a.team > 0,
        hint: this.isOwner
          ? 'עוד אדם שמפרסם שומר על הערוץ חי גם כשאין לכם זמן.'
          : 'בעלי הערוץ יכולים להוסיף כותבים ממסך הצוות.',
      },
    ];
  }

  toggleOpen(): void {
    this.open = !this.open;
  }

  dismiss(): void {
    this.activityService.dismiss(this.slug, 3);
    this.hidden = true;
  }

  markSeen(): void {
    const key = this.cardKey;
    if (!key) return;
    this.activityService.markSeen(this.slug, key);
    this.seenKey = key;
    this.open = false;
  }

  async shareLink(): Promise<void> {
    const url = this.share.channelUrl(this.slug);
    const outcome = await this.share.share({ title: this.channelName, text: this.share.inviteText(this.channelName, url), url });
    if (outcome === 'unsupported' && await this.share.copy(url)) {
      this.toastr.success('', 'הקישור לערוץ הועתק — אפשר להדביק ולשלוח');
    }
  }

  whatsappUrl(): string {
    return this.share.whatsappUrl(this.share.inviteText(this.channelName, this.share.channelUrl(this.slug)));
  }

  async setMails(on: boolean): Promise<void> {
    if (!this.activity || this.savingMails) return;
    this.savingMails = true;
    try {
      await this.activityService.setMailsOff(this.slug, !on);
      this.activity = { ...this.activity, mailsOff: !on };
      this.toastr.success('', on ? 'תזכורות במייל הופעלו' : 'תזכורות במייל כובו');
    } catch {
      this.toastr.danger('', 'השמירה נכשלה — נסו שוב');
    } finally {
      this.savingMails = false;
    }
  }

  manageLink(tab: string): string[] {
    return ['/channel', this.slug, 'manage', tab];
  }
}
