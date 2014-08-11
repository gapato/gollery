import Picture = require('picture');

class Album {
	constructor(public name: string, public cover: string, public hasSubdirs: boolean, public pictures: Picture[]) {
	}
}

export = Album;
