import { Token, Tokens, TokensList } from "marked";
import { MarkdownModuleConfig, MARKED_EXTENSIONS, MARKED_OPTIONS, MarkedRenderer } from "ngx-markdown";
import DOMPurify from "dompurify";

/**
 * Embed token: `[image-embedded#](url)`, optionally carrying the media's pixel
 * size as `[image-embedded#800x600](url)`.
 *
 * The size group is optional so every message written before the backend
 * started recording dimensions still tokenizes exactly as it did — it simply
 * renders without an aspect ratio, as it always has. The size lives in the
 * token rather than in the URL so the `src` stays byte-identical to the path
 * the server handed out.
 */
const matchCustomEmbedRegEx = /^\[(video|audio|image|quote)-embedded#(\d+x\d+)?]\((.*?)\)/;

/**
 * The embed token's opening bracket, without the payload — for callers that
 * only need to know a message *is* an embed and of which kind (capture group 1).
 *
 * Exported so it cannot drift from the tokenizer above. When the optional size
 * was added, a private copy of this pattern in the message component still
 * required `#]` immediately, so an image posted with its dimensions stopped
 * being recognised and quoting one embedded raw markdown into the reply.
 */
export const EMBED_PREFIX_REGEX = /^\[(video|audio|image|quote)-embedded#(?:\d+x\d+)?]/;

//https://regexr.com/3dj5t
const matchYoutubeRegEx = /^((?:https?:)?\/\/)?((?:www|m)\.)?((?:youtube\.com|youtu.be))(\/(?:[\w\-]+\?v=|embed\/|v\/)?)(?<id>[\w\-]+)(\S+)?$/;

/**
 * Every image the feed renders goes out with native lazy loading: messages
 * arrive 20 at a time through nbInfiniteList, but without this each one's
 * media is fetched the moment the message enters the DOM, so a media-heavy
 * channel opens by downloading every picture in the batch at once.
 *
 * `decoding="async"` keeps the decode off the main thread, so a picture
 * arriving mid-scroll cannot stall the list.
 *
 * Lazy loading alone makes the feed grow as pictures arrive, which shoves the
 * reader's position around. `sizeAttrs` below prevents that wherever the size
 * is known.
 */
const LAZY_IMG_ATTRS = ' loading="lazy" decoding="async"';

/**
 * Renders a `WxH` size token as width/height attributes. Bootstrap's
 * `.img-fluid` is `max-width: 100%; height: auto`, so the browser uses the
 * pair purely as an aspect ratio: the picture still fits the 300px box, but
 * the box reserves the right height before the bytes land and nothing below it
 * jumps when they do.
 *
 * An absent or malformed size yields no attributes at all — every message
 * predating dimension recording, and any upload whose header could not be
 * parsed, keeps exactly the old behaviour rather than getting a guessed ratio
 * that would letterbox or distort it.
 */
function sizeAttrs(size: string | undefined): string {
  const m = /^(\d+)x(\d+)$/.exec(size ?? '');
  if (!m) return '';
  const w = Number(m[1]), h = Number(m[2]);
  if (!w || !h) return '';
  return ` width="${w}" height="${h}"`;
}


/**
 * Everything below builds HTML by string concatenation and the feed renders it
 * with `[disableSanitizer]="true"`, so Angular does not get a second look at
 * it. Every value interpolated into markup therefore has to be neutralised
 * here — this is the only line of defence.
 *
 * Without it, message text was executable: `[image-embedded#](x" onerror="...)`
 * closed the src attribute and added an event handler, and the quote branch
 * interpolated free text straight into the document. Any writer could run
 * script in every viewer's browser, including a channel owner's or a super
 * admin's.
 */
function escapeHtml(value: unknown): string {
  return String(value ?? '')
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
    .replace(/'/g, '&#39;');
}

/**
 * Neutralises a URL destined for a src/href attribute.
 *
 * A scheme is only honoured when it is http or https, so `javascript:`,
 * `data:` and `vbscript:` collapse to an empty attribute instead of executing.
 * Relative URLs — which is what the upload path produces
 * (/api/channel/<slug>/files/<id>) — carry no scheme and pass through. Control
 * characters are dropped first, since `java\tscript:` is the same URL to a
 * browser but not to a naive prefix test.
 */
function safeUrl(value: unknown): string {
  const url = String(value ?? '').replace(/[\u0000-\u001F\u007F]/g, '').trim();
  const scheme = /^([a-z][a-z0-9+.-]*):/i.exec(url);
  if (scheme && !/^https?$/i.test(scheme[1])) return '';
  return escapeHtml(url);
}

const customEmbedExtension = {
  extensions: [{
    name: 'custom_embed',
    level: 'inline',
    start: (src: string) => src.match(matchCustomEmbedRegEx)?.index ?? src.match(matchYoutubeRegEx)?.index,
    tokenizer: (src: string, tokens: Token[] | TokensList) => {

      let match = src.match(matchCustomEmbedRegEx);
      if (match) {
        switch (match[1]) {
          case 'quote':
            const s = match[3].split(/@(.*)/);
            return {
              type: 'custom_embed',
              raw: match[0],
              meta: { type: 'quote', id: s[0], url: s[1] },
            };

          default:
            return {
              type: 'custom_embed',
              raw: match[0],
              meta: { type: match[1], url: match[3], size: match[2] },
            };
        }

      }

      match = src.match(matchYoutubeRegEx);
      if (match && match.groups?.['id']) {
        return {
          type: 'custom_embed',
          raw: match[0],
          meta: { type: 'youtube', id: match.groups['id'] },
        };
      }

      return undefined;
    },
    renderer: (token: Tokens.Generic) => {
      const { type, url, id, size } = token['meta'];
      switch (type) {
        case 'video':
          // metadata, not auto: enough for the poster frame and duration
          // without pulling the whole file for a message nobody scrolled to.
          // The size token is not emitted for video, so this is inert today;
          // it costs nothing and lets a recorded size reserve the player box
          // if the upload path ever measures video too.
          return `<div style="max-width: 300px; height: auto;"><video controls preload="metadata" style="width: 100%; height: auto;"${sizeAttrs(size)}><source src="${safeUrl(url)}" type="video/mp4"></video></div>`;
        case 'audio':
          return `<div><audio src="${safeUrl(url)}" controls preload="metadata"></audio></div>`;
        case 'image':
          // The real size when the upload path recorded it, so the box
          // reserves the right height; the old flat width="300" otherwise.
          return `<div style="max-width: 300px; height: auto;"><img src="${safeUrl(url)}"${LAZY_IMG_ATTRS} class="img-fluid"${sizeAttrs(size) || ' width="300"'}></div>`;
        case 'youtube':
          return `<div style="position: relative; max-width: 300px; height: auto;"><img youtubeid="${escapeHtml(id)}" src="https://ytimg.googleusercontent.com/vi/${encodeURIComponent(String(id))}/hqdefault.jpg"${LAZY_IMG_ATTRS} class="img-fluid" width="300" height="225"><i
          class="bi bi-youtube" youtubeid="${escapeHtml(id)}" style="position: absolute; place-self: anchor-center; color: red; font-size: 70px;"></i></div>`;
        case 'quote':
          return `<blockquote class="quote" quote-id="${escapeHtml(id)}"><p>${escapeHtml(url)}</p></blockquote>`;
        default:
          return '';
      }
    }
  }]
}

const renderer = new MarkedRenderer();

//https://github.com/jfcere/ngx-markdown/issues/79#issuecomment-2484682034
renderer.link = ({ href, text }) => {
  // Overriding marked's link renderer also discards its escaping and its
  // cleanUrl scheme check, so both have to be reapplied here.
  return `<a target="_blank" rel="noopener noreferrer" href="${safeUrl(href)}">${escapeHtml(text)}</a>`;
}

// Plain markdown images (![alt](url)) bypass the custom_embed extension, so
// they need the same lazy treatment — this is the second and last place the
// feed can emit an <img>.
renderer.image = ({ href, title, text }) => {
  const titleAttr = title ? ` title="${escapeHtml(title)}"` : '';
  return `<img src="${safeUrl(href)}" alt="${escapeHtml(text)}"${titleAttr}${LAZY_IMG_ATTRS} class="img-fluid">`;
}
//renderer.paragraph = ({ tokens }) => Parser.parseInline(tokens);

/**
 * Final sanitization of the rendered feed HTML.
 *
 * The feed binds this output with `[disableSanitizer]="true"`, so Angular never
 * gets a second look — and marked passes RAW HTML in the message body straight
 * through its default renderer. Per-value escaping in the custom renderers above
 * only covers what THEY build; a message body of `<img src=x onerror=alert(1)>`
 * or `<a href="javascript:...">` reached the DOM verbatim and executed. This is
 * the actual trust boundary, and it has to allow the small set of markup the
 * feed legitimately produces (the media/quote/link embeds, and the `<u>` the
 * formatting toolbar inserts) while removing everything dangerous.
 *
 * DOMPurify strips event handlers and javascript:/data: URLs by default; the
 * config below additionally forbids elements the feed never needs and that are
 * common injection vectors, and re-permits the two non-standard attributes the
 * embeds rely on (youtubeid, quote-id) plus target/rel on links.
 */
/**
 * DOMPurify allows `data:` URIs on media tags (img/video/audio/source)
 * independent of ALLOWED_URI_REGEXP, as a legitimate way to inline images. The
 * feed never inlines media that way — every image is a relative /api/... URL or
 * an https thumbnail — so this hook strips any src/href that is not http(s),
 * mailto, or relative. A `data:text/html` src on an img is inert on its own (an
 * img never runs its src as a document), but removing it leaves no room for
 * doubt and matches the backend's own scheme allow-list.
 */
DOMPurify.addHook('afterSanitizeAttributes', (node: Element) => {
  for (const attr of ['src', 'href']) {
    const value = node.getAttribute?.(attr);
    if (value === null || value === undefined) continue;
    const scheme = /^([a-z][a-z0-9+.-]*):/i.exec(value.trim());
    if (scheme && !/^(https?|mailto)$/i.test(scheme[1])) {
      node.removeAttribute(attr);
    }
  }
});

function sanitizeFeedHtml(html: string): string {
  return DOMPurify.sanitize(html, {
    // Non-standard attributes the embed renderers emit and the message
    // component reads (message.component.ts reads youtubeid); target/rel are on
    // the link renderer's anchors.
    ADD_ATTR: ['youtubeid', 'quote-id', 'target', 'rel'],
    // The feed emits none of these, and each is a classic injection vector.
    // Event handlers and dangerous URL schemes are removed by DOMPurify
    // regardless of this list.
    FORBID_TAGS: ['style', 'iframe', 'form', 'input', 'button', 'object', 'embed', 'svg', 'math', 'link', 'meta', 'base'],
    // Only the schemes the feed actually uses: https/http (youtube, external
    // links), mailto, and relative URLs (uploaded files are /api/channel/...).
    // This also removes the inert-but-pointless data: image case, matching the
    // backend's own safeUrl scheme allow-list.
    ALLOWED_URI_REGEXP: /^(?:https?:|mailto:|[^a-z]|[a-z+.-]+(?:[^a-z+.\-:]|$))/i,
  }) as unknown as string;
}

export const MarkdownConfig: MarkdownModuleConfig = {
  markedExtensions: [
    {
      provide: MARKED_EXTENSIONS,
      useValue: customEmbedExtension,
      multi: true,
    },
  ],
  markedOptions: {
    provide: MARKED_OPTIONS,
    useValue: {
      renderer: renderer,
      breaks: true,
      // Runs after marked has produced the HTML — including any raw HTML in the
      // message body — so it is the last thing before the string is bound.
      hooks: {
        postprocess: (html: string) => sanitizeFeedHtml(html),
      },
    },
  }
}