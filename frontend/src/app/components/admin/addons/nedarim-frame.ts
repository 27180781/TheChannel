/**
 * Nedarim Plus's payment iframe (the same protocol Magnet's checkout speaks):
 * Magnet's billing opened the standing order with the amount locked, and the
 * page hands the iframe only the transaction's ID and Key. The owner types
 * the card inside the iframe; this page never sees it.
 *
 * The iframe's own "success" is for the screen only. The add-on is paid when
 * Nedarim's callback has reached Magnet, which the status then shows.
 */

/** Always with www: Nedarim's note. */
export const NEDARIM_ORIGIN = 'https://www.matara.pro';
export const NEDARIM_FRAME_SRC = `${NEDARIM_ORIGIN}/nedarimplus/iframe/v3/?language=he`;

/** A standing order is a card one. */
const NEDARIM_METHODS = 'Card';

export type NedarimFrameEvent =
  | { type: 'ready' }
  | { type: 'height'; px: number }
  | { type: 'back' }
  | { type: 'pending' }
  | { type: 'result'; ok: boolean; message: string | null };

/** A message from Nedarim's iframe, or null for anything else. */
export function parseNedarimMessage(ev: Pick<MessageEvent, 'origin' | 'data'>): NedarimFrameEvent | null {
  if (ev.origin !== NEDARIM_ORIGIN) return null;
  const d = ev.data as { Name?: unknown; Value?: unknown } | null;
  if (!d || typeof d !== 'object' || typeof d.Name !== 'string') return null;
  const v = d.Value as Record<string, unknown> | null | undefined;
  switch (d.Name) {
    case 'Ready':
      return { type: 'ready' };
    case 'Height': {
      const px = parseInt(String(d.Value), 10);
      return Number.isFinite(px) && px > 0 ? { type: 'height', px } : null;
    }
    case 'Back':
      return { type: 'back' };
    case 'PendingTransaction':
      return { type: 'pending' };
    case 'TransactionResponse':
      return {
        type: 'result',
        ok: v?.['Status'] === 'OK',
        message: typeof v?.['Message'] === 'string' ? (v['Message'] as string).replace(/<[^>]*>/g, ' ').trim() || null : null,
      };
    default:
      return null;
  }
}

export function startPaymentMessage(transactionId: string, key: string) {
  return {
    Name: 'StartPayment',
    Value: { TransactionId: transactionId, Key: key, Methods: NEDARIM_METHODS, ButtonText: 'הצטרפות למנוי' },
  };
}

/** An Israeli ID number's check digit, the rule Magnet applies before opening the order. */
export function isValidIsraeliId(raw: string): boolean {
  const id = raw.replace(/[\s-]/g, '');
  if (!/^\d{5,9}$/.test(id)) return false;
  const p = id.padStart(9, '0');
  if (p === '000000000') return false;
  let sum = 0;
  for (let i = 0; i < 9; i++) {
    let d = Number(p[i]) * ((i % 2) + 1);
    if (d > 9) d -= 9;
    sum += d;
  }
  return sum % 10 === 0;
}
