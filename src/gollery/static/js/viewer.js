define(['common', 'hammer', 'jquery'], function(Common, Hammer, $) {

function Viewer(app) {
	var viewer = this;

	viewer.app = app;

	$('#viewer-quit-button').click(function() {
		viewer.goBackToAlbums();
	});

	$('#viewer-prev-button').click(function() {
		viewer.viewSibling(-1);
	});

	$('#viewer-next-button').click(function() {
		viewer.viewSibling(1);
	});

	$(document).keyup(function(ev) {
		// Don't do anything if we're not watching pictures
		if (!viewer.filename) {
			return;
		}

		switch (ev.keyCode) {
		case 27: // Escape
			viewer.goBackToAlbums();
			break;
		case 37: // Left
			viewer.viewSibling(-1);
			break;
		case 39: // Right
			viewer.viewSibling(1);
			break;
		}
	});

	var h = $('#viewer-inner').hammer();

	h.on('swiperight', function() {
		viewer.viewSibling(-1);
	});

	h.on('swipeleft', function() {
		viewer.viewSibling(1);
	});

	Common.dontScroll('#viewer');

	if (viewer.supportsFullscreen()) {
		$fullscreen_button = $('#viewer-fullscreen-button');
		$fullscreen_button.css('display', 'inline-block');

		$fullscreen_button.click(function() {
			viewer.goFullscreen();
		});
	}
}

Viewer.prototype = {
	goBackToAlbums: function() {
		if (!this.album) {
			document.location.hash = '';
			return;
		}

		if (this.previousRoute) {
			document.location.hash = this.app.buildHash(this.previousRoute);
			this.previousRoute = null;
			return;
		}

		document.location.hash = '#browse:' + this.album.name;

		this.album = null;
		this.filename = null;
	},

	view: function(album, filename) {
		this.album = album;
		this.filename = filename;

		if (this.app.previousRoute && this.app.previousRoute.action !== 'view') {
			this.previousRoute = this.app.previousRoute;
		}

		var imgUrl = document.location.origin + '/thumbnails/large/' + album.name + '/' + filename;

		$('#viewer-img').attr('src', '');
		$('#viewer-img').attr('src', imgUrl);
	},

	viewSibling: function(direction) {
		direction = (direction > 0 ? 1 : -1);

		var pics = this.album.pictures;
		var filename = this.filename;

		var idx = -1;

		for (i = 0; i < pics.length; ++i) {
			if (pics[i].path === filename) {
				idx = i;
				break;
			}
		}

		if (idx === -1) {
			console.log('Cannot find ' + filename + ' in pictures of album ' + album.name);
			return;
		}

		idx += direction;

		if (idx >= pics.length) {
			idx -= pics.length;
		}

		if (idx < 0) {
			idx += pics.length;
		}

		document.location.hash = 'view:' + encodeURIComponent(this.album.name) + '/' + pics[idx].path;
	},

	supportsFullscreen: function() {
		return document.documentElement.requestFullscreen ||
			document.documentElement.mozRequestFullScreen ||
			document.documentElement.webkitRequestFullscreen;
	},

	goFullscreen: function() {
		// Stolen from https://developer.mozilla.org/en-US/docs/Web/Guide/API/DOM/Using_full_screen_mode
		if (document.documentElement.requestFullscreen) {
			document.documentElement.requestFullscreen();
		} else if (document.documentElement.mozRequestFullScreen) {
			document.documentElement.mozRequestFullScreen();
		} else if (document.documentElement.webkitRequestFullscreen) {
			document.documentElement.webkitRequestFullscreen(Element.ALLOW_KEYBOARD_INPUT);
		}
	}
};

return Viewer;

}); // define
