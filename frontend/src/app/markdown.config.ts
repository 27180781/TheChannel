import { Token, Tokens, TokensList } from "marked";
import { MarkdownModuleConfig, MARKED_EXTENSIONS, MARKED_OPTIONS, MarkedRenderer } from "ngx-markdown";

const matchCustomEmbedRegEx = /^\[(video|audio|image|quote)-embedded#]\((.*?)\)/;

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
 * No width/height pair is emitted for uploaded images on purpose: the backend
 * stores only URL, filename and MIME type per file (backend/files.go
 * FileResponse/FileMetadata), so the real pixel size is unknown at render
 * time. Guessing one would letterbox or distort the picture, which is worse
 * than the reflow. Images are already boxed to max-width 300px, so the shift
 * is vertical only; emitting exact dimensions needs the upload path to record
 * them first.
 */
const LAZY_IMG_ATTRS = ' loading="lazy" decoding="async"';

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
            const s = match[2].split(/@(.*)/);
            return {
              type: 'custom_embed',
              raw: match[0],
              meta: { type: 'quote', id: s[0], url: s[1] },
            };

          default:
            return {
              type: 'custom_embed',
              raw: match[0],
              meta: { type: match[1], url: match[2] },
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
      const { type, url, id } = token['meta'];
      switch (type) {
        case 'video':
          // metadata, not auto: enough for the poster frame and duration
          // without pulling the whole file for a message nobody scrolled to.
          return `<div style="max-width: 300px; height: auto;"><video controls preload="metadata" style="width: 100%; height: auto;"><source src="${url}" type="video/mp4"></video></div>`;
        case 'audio':
          return `<div><audio src="${url}" controls preload="metadata"></audio></div>`;
        case 'image':
          return `<div style="max-width: 300px; height: auto;"><img src="${url}"${LAZY_IMG_ATTRS} class="img-fluid" width="300"></div>`;
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