import { HttpClient } from '@angular/common/http';
import { Injectable } from '@angular/core';
import { BehaviorSubject, firstValueFrom, Observable, Subject } from 'rxjs';
import { ChatFile, ChatMessage } from './chat.service';
import { ResponseResult } from '../models/response-result.model';
import { Setting } from '../models/setting.model';
import { Reports, Report } from '../models/report.model';
import { Statistics } from '../models/statistics.model';
import { SlugService } from './slug.service';
import { ChannelUser } from '../models/channel-user.model';

export type { ChannelUser };

export type EditMsg = {
  new?: boolean;
  isScheduling?: boolean;
  message: ChatMessage;
}

@Injectable({
  providedIn: 'root'
})
export class AdminService {
  private messageEdit = new BehaviorSubject<EditMsg | undefined>(undefined);
  messageEditObservable = this.messageEdit.asObservable();

  private schedulingBus = new Subject<void>();
  schedulingBusObservable = this.schedulingBus.asObservable();

  private schedulingMessages: ChatMessage[] | null = null;

  constructor(
    private http: HttpClient,
    private slugService: SlugService,
  ) { }

  private get slug() { return this.slugService.slug; }

  clearCache() {
    this.schedulingMessages = null;
  }

  reloadSchedulingMessage() {
    this.schedulingBus.next();
  }

  setEditMessage(edit: EditMsg | undefined) {
    this.messageEdit.next(edit);
  }

  getEditMessage(): EditMsg | undefined {
    return this.messageEdit.value;
  }

  getStatistics(): Promise<Statistics> {
    return firstValueFrom(this.http.get<Statistics>(`/api/channel/${this.slug}/admin/statistics`));
  }

  addMessage(message: ChatMessage): Observable<ChatMessage> {
    return this.http.post<ChatMessage>(`/api/channel/${this.slug}/admin/new`, message);
  }

  // Both endpoints answer with the {success} envelope, not the message itself.
  editMessage(message: ChatMessage): Observable<ResponseResult> {
    return this.http.post<ResponseResult>(`/api/channel/${this.slug}/admin/edit-message`, message);
  }

  deleteMessage(id: number | undefined): Observable<ResponseResult> {
    return this.http.delete<ResponseResult>(`/api/channel/${this.slug}/admin/delete-message/${id}`);
  }

  uploadFile(formData: FormData) {
    return this.http.post<ChatFile>(`/api/channel/${this.slug}/admin/upload`, formData, {
      reportProgress: true,
      observe: 'events',
      responseType: 'json'
    });
  }

  getChannelUsers(): Promise<ChannelUser[]> {
    return firstValueFrom(this.http.get<ChannelUser[]>(`/api/channel/${this.slug}/admin/users/get`));
  }

  setChannelUsers(users: ChannelUser[]): Promise<ResponseResult> {
    return firstValueFrom(this.http.post<ResponseResult>(`/api/channel/${this.slug}/admin/users/set`, { users }));
  }

  setEmojis(emojis: string[] | undefined) {
    return firstValueFrom(this.http.post<ResponseResult>(`/api/channel/${this.slug}/admin/set-emojis`, { emojis }));
  }

  getSettings(): Promise<Setting[]> {
    return firstValueFrom(this.http.get<Setting[]>(`/api/channel/${this.slug}/admin/settings/get`));
  }

  setSettings(settings: Setting[]): Promise<ResponseResult> {
    return firstValueFrom(this.http.post<ResponseResult>(`/api/channel/${this.slug}/admin/settings/set`, settings));
  }

  getReports(status: string): Promise<Reports> {
    return firstValueFrom(this.http.get<Reports>(`/api/channel/${this.slug}/admin/reports/get`, {
      params: {
        status: status
      }
    }));
  }

  setReports(report: Report): Promise<ResponseResult> {
    return firstValueFrom(this.http.post<ResponseResult>(`/api/channel/${this.slug}/admin/reports/set`, report));
  }

  private fetchScheduledMessages(): Promise<ChatMessage[]> {
    return firstValueFrom(this.http.get<ChatMessage[]>(`/api/channel/${this.slug}/admin/scheduled-messages/get`));
  }

  async getScheduledMessages(reload?: boolean): Promise<ChatMessage[]> {
    if (this.schedulingMessages && !reload) {
      return this.schedulingMessages;
    }

    try {
      this.schedulingMessages = await this.fetchScheduledMessages();
      return this.schedulingMessages;
    } catch {
      return this.schedulingMessages || [];
    }
  }

  async setScheduledMessage(message: ChatMessage): Promise<ResponseResult> {
    // The update endpoint replaces the whole list, so the cache must be loaded
    // first — otherwise an empty/null cache posts an empty body (400) or wipes
    // the messages already scheduled. A failed load rejects instead of saving.
    const list = this.schedulingMessages ?? await this.fetchScheduledMessages();
    this.schedulingMessages = list;
    return this.commitSchedulingMessages([message, ...list]);
  }

  editScheduledMessage(message: ChatMessage): Promise<ResponseResult> {
    const list = this.schedulingMessages;
    if (!list || !this.isScheduledIndex(message.id, list)) return Promise.reject('Message ID is undefined');
    const next = list.slice();
    next[message.id] = message;
    return this.commitSchedulingMessages(next);
  }

  deleteScheduledMessage(id: number | undefined): Promise<ResponseResult> {
    const list = this.schedulingMessages;
    if (!list || !this.isScheduledIndex(id, list)) return Promise.reject('Message ID is undefined');
    return this.commitSchedulingMessages(list.filter((_, i) => i !== id));
  }

  /**
   * A scheduled message's id is its index in the list (the feed stamps it on
   * edit). Anything else is refused: a LIVE message's id written at that
   * index used to punch hundreds of null holes into a short array, the POST
   * came back 400, and the corrupted list stayed on screen until reload.
   */
  private isScheduledIndex(id: number | undefined, list: ChatMessage[]): id is number {
    return Number.isInteger(id) && (id as number) >= 0 && (id as number) < list.length;
  }

  /**
   * Copy, post, then commit. The list is shared with the feed's scheduled
   * section, so mutating it before the POST left every viewer of it with a
   * list the server had rejected. Callers refresh the feed through
   * reloadSchedulingMessage() once this resolves.
   */
  private async commitSchedulingMessages(next: ChatMessage[]): Promise<ResponseResult> {
    const res = await firstValueFrom(this.http.post<ResponseResult>(`/api/channel/${this.slug}/admin/scheduled-messages/update`, next));
    this.schedulingMessages = next;
    return res;
  }
}
