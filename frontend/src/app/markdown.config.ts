import { Token, Tokens, TokensList } from "marked";
import { MarkdownModuleConfig, MARKED_EXTENSIONS, MARKED_OPTIONS, MarkedRenderer } from "ngx-markdown";

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
          return `<div style="max-width: 300px; height: auto;"><video controls preload="metadata" style="width: 100%; height: auto;"${sizeAttrs(size)}><source src="${url}" type="video/mp4"></video></div>`;
        case 'audio':
          return `<div><audio src="${url}" controls preload="metadata"></audio></div>`;
        case 'image':
          // The real size when the upload path recorded it, so the box
          // reserves the right height; the old flat width="300" otherwise.
          return `<div style="max-width: 300px; height: auto;"><img src="${url}"${LAZY_IMG_ATTRS} class="img-fluid"${sizeAttrs(size) || ' width="300"'}></div>`;
        case 'youtube':
          return `<div style="position: relative; max-width: 300px; height: auto;"><img youtubeid="${id}" src="https://ytimg.googleusercontent.com/vi/${id}/hqdefault.jpg"${LAZY_IMG_ATTRS} class="img-fluid" width="300" height="225"><i
          class="bi bi-youtube" youtubeid="${id}" style="position: absolute; place-self: anchor-center; color: red; font-size: 70px;"></i></div>`;
        case 'quote':
          return `<blockquote class="quote" quote-id="${id}"><p>${url}</p></blockquote>`;
        default:
          return '';
      }
    }
  }]
}

const renderer = new MarkedRenderer();

//https://github.com/jfcere/ngx-markdown/issues/79#issuecomment-2484682034
renderer.link = ({ href, text }) => {
  return `<a target="_blank" href="${href}">${text}</a>`;
}

// Plain markdown images (![alt](url)) bypass the custom_embed extension, so
// they need the same lazy treatment — this is the second and last place the
// feed can emit an <img>.
renderer.image = ({ href, title, text }) => {
  const titleAttr = title ? ` title="${title}"` : '';
  return `<img src="${href}" alt="${text ?? ''}"${titleAttr}${LAZY_IMG_ATTRS} class="img-fluid">`;
}
//renderer.paragraph = ({ tokens }) => Parser.parseInline(tokens);

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
    },
  }
}