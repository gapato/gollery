import Album = require('album');
import App = require('app');
import Common = require('common');
import LoadingScreen = require('loadingscreen');

class AlbumFrame {
	private static DefaultCoverUrl = '/images/camera-roll.png';

	public el: HTMLElement;

	constructor(app: App, album: Album, href: string) {
		var frame = document.createElement('div');
		frame.className = 'album-frame';

		var a = document.createElement('a');
		a.className = 'album-frame-inner';

		if (href) {
			a.href = href;
		}

		frame.appendChild(a);

		var span = document.createElement('span');
		span.className = 'loading';

		a.appendChild(span);

		var img = document.createElement('img');

		img.addEventListener('load', function() {
			span.className = '';
		});

		img.addEventListener('error', function() {
			app.oops('Cannot load image ' + img.src);
		});

		img.src = AlbumFrame.DefaultCoverUrl;

		if (album.cover) {
			img.src = Common.pictureUrl('small', album.cover);
		}

		span.appendChild(img);

		var titleFrame = document.createElement('a');
		titleFrame.href = href;
		titleFrame.className = 'album-frame-title';
		frame.appendChild(titleFrame);

		var title = album.name.slice(album.name.lastIndexOf('/')+1);
		titleFrame.appendChild(document.createTextNode(title));

		this.el = frame;
	}
}

export = AlbumFrame;
