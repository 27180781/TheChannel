import { HttpClient } from '@angular/common/http';
import { Injectable } from '@angular/core';
import { Observable, firstValueFrom } from 'rxjs';

export type SlugUnavailableReason = 'taken' | 'invalid' | 'reserved' | '';

export interface SlugAvailability {
  available: boolean;
  reason: SlugUnavailableReason;
}

export interface CreatedChannel {
  slug: string;
  name: string;
}

/**
 * Mirror of the backend slug rule (`^[a-z0-9][a-z0-9\-]{1,48}[a-z0-9]$`): 3–50
 * characters, lowercase alphanumerics and hyphens, never starting or ending
 * with a hyphen. Kept here so the form can pre-validate before spending a
 * round-trip on /slug-available.
 */
export const SLUG_PATTERN = /^[a-z0-9][a-z0-9-]{1,48}[a-z0-9]$/;

/**
 * Derives a slug suggestion from a free-text channel name: lowercase, spaces to
 * hyphens, anything outside [a-z0-9-] dropped. Hebrew names collapse to an
 * empty string — the caller then simply leaves the slug for the user to type.
 */
export function slugifyChannelName(name: string): string {
  return (name || '')
    .toLowerCase()
    .trim()
    .replace(/\s+/g, '-')
    .replace(/[^a-z0-9-]/g, '')
    .replace(/-{2,}/g, '-')
    .replace(/^-+|-+$/g, '')
    .slice(0, 50);
}

@Injectable({
  providedIn: 'root'
})
export class ChannelService {

  constructor(private http: HttpClient) {}

  /**
   * Instant channel creation. The owner is taken from the session on the
   * backend, so there is deliberately no owner field here.
   * 400 invalid slug / missing name · 409 slug taken · 403 channel limit ·
   * 429 rate limited — all answered as plain text in the body.
   */
  createChannel(slug: string, name: string, description: string): Promise<CreatedChannel> {
    return firstValueFrom(
      this.http.post<CreatedChannel>('/api/channels/create', { slug, name, description })
    );
  }

  /** Left as an Observable so callers can debounce/switchMap the keystrokes. */
  checkSlugAvailability(slug: string): Observable<SlugAvailability> {
    return this.http.get<SlugAvailability>('/api/channels/slug-available', { params: { slug } });
  }
}
