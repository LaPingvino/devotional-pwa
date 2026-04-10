// add_divine_plan_fulltext.go — Insert Tablets of the Divine Plan into inventory_fulltext
// Source: https://www.bahai.org/library/authoritative-texts/abdul-baha/tablets-divine-plan/
//
// Usage: go run add_divine_plan_fulltext.go [--dry-run]
package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

const doltDir = "/home/joop/bahaiwritings"
const partSize = 850

type tablet struct {
	pin  string
	text string
}

var tablets = []tablet{
	{"AB00956", `O ye heavenly heralds:

These are the days of Naw-Rúz. I am always thinking of those kind friends! I beg for each and all of you confirmations and assistance from the threshold of oneness, so that those gatherings may become ignited like unto candles, in the republics of America, enkindling the light of the love of God in the hearts; thus the rays of the heavenly teachings may begem and brighten the states of America like the infinitude of immensity with the stars of the Most Great Guidance.

The Northeastern States on the shores of the Atlantic—Maine, New Hampshire, Massachusetts, Rhode Island, Connecticut, Vermont, Pennsylvania, New Jersey and New York—in some of these states believers are found, but in some of the cities of these states up to this date people are not yet illumined with the lights of the Kingdom and are not aware of the heavenly teachings; therefore, whenever it is possible for each one of you, hasten ye to those cities and shine forth like unto the stars with the light of the Most Great Guidance. God says in the glorious Qur'án: "The soil was black and dried. Then we caused the rain to descend upon it and immediately it became green, verdant, and every kind of plant sprouted up luxuriantly." In other words, He says the earth is black, but when the spring showers descend upon it that black soil is quickened, and variegated flowers are pushed forth. This means the souls of humanity belonging to the world of nature are black like unto the soil. But when the heavenly outpourings descend and the radiant effulgences appear, the hearts are resuscitated, are liberated from the darkness of nature and the flowers of divine mysteries grow and become luxuriant. Consequently man must become the cause of the illumination of the world of humanity and propagate the holy teachings revealed in the sacred books through the divine inspiration. It is stated in the blessed Gospel: Travel ye toward the East and toward the West and enlighten the people with the light of the Most Great Guidance, so that they may take a portion and share of eternal life. Praise be to God, that the Northeastern States are in the utmost capacity. Because the ground is rich, the rain of the divine outpouring is descending. Now you must become heavenly farmers and scatter pure seeds in the prepared soil. The harvest of every other seed is limited, but the bounty and the blessing of the seed of the divine teachings is unlimited. Throughout the coming centuries and cycles many harvests will be gathered. Consider the work of former generations. During the lifetime of Jesus Christ the believing, firm souls were few and numbered, but the heavenly blessings descended so plentifully that in a number of years countless souls entered beneath the shadow of the Gospel. God has said in the Qur'án: "One grain will bring forth seven sheaves, and every sheaf shall contain one hundred grains." In other words, one grain will become seven hundred; and if God so wills He will double these also. It has often happened that one blessed soul has become the cause of the guidance of a nation. Now we must not consider our ability and capacity, nay, rather, we must fix our gaze upon the favors and bounties of God, in these days, Who has made of the drop a sea, and of the atom a sun.

Upon you be greeting and praise!`},

	{"AB01505", `O ye heralds of the Kingdom of God:

A few days ago an epistle was written to those divine believers, but because these days are the days of Naw-Rúz, you have come to my mind and I am sending you this greeting for this glorious feast. All the days are blessed, but this feast is the national fête of Persia. The Persians have been holding it for several thousand years past. In reality every day which man passes in the mention of God, the diffusion of the fragrances of God and calling the people to the Kingdom of God, that day is his feast. Praise be to God that you are occupied in the service of the Kingdom of God and are engaged in the promulgation of the religion of God by day and by night. Therefore all your days are feast days. There is no doubt that the assistance and the bestowal of God shall descend upon you.

In the Southern States of the United States, the friends are few, that is, in Delaware, Maryland, Virginia, West Virginia, North Carolina, South Carolina, Georgia, Florida, Alabama, Mississippi, Tennessee, Kentucky, Louisiana, Arkansas, Oklahoma and Texas. Consequently you must either go yourselves or send a number of blessed souls to those states, so that they may guide the people to the Kingdom of Heaven. One of the holy Manifestations, addressing a believing soul, has said that, if a person become the cause of the illumination of one soul, it is better than a boundless treasury. "O 'Alí! If God guide, through thee, one soul, it is better for thee than all the riches!" Again He says, "Direct us to the straight path!" that is, Show us the right road. It is also mentioned in the Gospel: Travel ye to all parts of the world and give ye the glad tidings of the appearance of the Kingdom of God.

In brief, I hope you will display in this respect the greatest effort and magnanimity. It is assured that you will become assisted and confirmed. A person declaring the glad tidings of the appearance of the realities and significances of the Kingdom is like unto a farmer who scatters pure seeds in the rich soil. The spring cloud will pour upon them the rain of bounty, and unquestionably the station of the farmer will be raised in the estimation of the lord of the village, and many harvests will be gathered.

Therefore, ye friends of God! Appreciate ye the value of this time and be ye engaged in the sowing of the seeds, so that you may find the heavenly blessing and the lordly bestowal. Upon you be Bahá'u'l-Abhá!`},

	{"AB01130", `O ye heavenly souls, O ye spiritual assemblies, O ye lordly meetings:

For some time past correspondence has been delayed, and this has been on account of the difficulty of mailing and receiving letters. But because at present a number of facilities are obtainable, therefore, I am engaged in writing you this brief epistle so that my heart and soul may obtain joy and fragrance through the remembrance of the friends. Continually this wanderer supplicates and entreats at the threshold of His Holiness the One and begs assistance, bounty and heavenly confirmations in behalf of the believers. You are always in my thoughts. You are not nor shall you ever be forgotten. I hope by the favor of His Holiness the Almighty that day by day you may add to your faith, assurance, firmness and steadfastness, and become instruments for the promotion of the holy fragrances.

Although in the states of Illinois, Wisconsin, Ohio, Michigan and Minnesota—praise be to God—believers are found who are associating with each other in the utmost firmness and steadfastness—day and night they have no other intention save the diffusion of the fragrances of God, they have no other hope except the promotion of the heavenly teachings, like the candles they are burning with the light of the love of God, and like thankful birds are singing songs, spirit-imparting, joy-creating, in the rose garden of the knowledge of God—yet in the states of Indiana, Iowa, Missouri, North Dakota, South Dakota, Nebraska and Kansas few of the believers exist. So far the summons of the Kingdom of God and the proclamation of the oneness of the world of humanity has not been made in these states systematically and enthusiastically. Blessed souls and detached teachers have not traveled through these parts repeatedly; therefore these states are still in a state of heedlessness. Through the efforts of the friends of God souls must be likewise enkindled in these states, with the fire of the love of God and attracted to the Kingdom of God, so that section may also become illumined and the soul imparting breeze of the rose garden of the Kingdom may perfume the nostrils of the inhabitants. Therefore, if it is possible, send to those parts teachers who are severed from all else save God, sanctified and pure. If these teachers be in the utmost state of attraction, in a short time great results will be forthcoming. The sons and daughters of the kingdom are like unto the real farmers. Through whichever state or country they pass they display self-sacrifice and sow divine seeds. From that seed harvests are produced. On this subject it is revealed in the glorious Gospel: When the pure seeds are scattered in the good ground heavenly blessing and benediction is obtained. I hope that you may become assisted and confirmed, and never lose courage in the promotion of the divine teachings. Day by day may you add to your effort, exertion, and magnanimity.

Upon you be greeting and praise!`},

	{"AB00936", `O ye sons and daughters of the Kingdom:

Day and night I have no other occupation than the remembrance of the friends, praying from the depth of my heart in their behalf, begging for them confirmation from the Kingdom of God and supplicating the direct effect of the breaths of the Holy Spirit. I am hopeful from the favors of His Highness the Lord of Bestowals, that the friends of God during such a time may become the secret cause of the illumination of the hearts of humanity, breathing the breath of life upon the spirits—whose praiseworthy results may become conducive to the glory and exaltation of humankind throughout all eternity. Although in some of the Western States, like California, Oregon, Washington and Colorado, the fragrances of holiness are diffused, numerous souls have taken a share and a portion from the fountain of everlasting life, they have obtained heavenly benediction, have drunk an overflowing cup from the wine of the love of God and have hearkened to the melody of the Supreme Concourse—yet in the states of New Mexico, Wyoming, Montana, Idaho, Utah, Arizona and Nevada, the lamp of the love of God is not ignited in a befitting and behooving manner, and the call of the Kingdom of God has not been raised. Now, if it is possible, show ye an effort in this direction. Either travel yourselves, personally, throughout those states or choose others and send them, so that they may teach the souls. For the present those states are like unto dead bodies: they must breathe into them the breath of life and bestow upon them a heavenly spirit. Like unto the stars they must shine in that horizon and thus the rays of the Sun of Reality may also illumine those states.

God says in the great Qur'án: "Verily God is the helper of those who have believed. He will lead them from darkness into light." This means: God loves the believers, consequently He will deliver them from darkness and bring them into the world of light.

It is also recorded in the blessed Gospel: Travel ye throughout the world and call ye the people to the Kingdom of God. Now this is the time that you may arise and perform this most great service and become the cause of the guidance of innumerable souls. Thus through this superhuman service the rays of peace and conciliation may illumine and enlighten all the regions and the world of humanity may find peace and composure.

During my stay in America I cried out in every meeting and summoned the people to the propagation of the ideals of universal peace. I said plainly that the continent of Europe had become like unto an arsenal and its conflagration was dependent upon one spark, and that in the coming years, or within two years, all that which is recorded in the Revelation of John and the Book of Daniel would become fulfilled and come to pass.

Upon you be greeting and praise!`},

	{"AB01552", `O ye daughters and sons of the Kingdom:

Although in most of the states and cities of the United States, praise be to God, His fragrances are diffused, and souls unnumbered are turning their faces and advancing toward the Kingdom of God, yet in some of the states the Standard of Unity is not yet upraised as it should be, nor are the mysteries of the Holy Books, such as the Bible, the Gospel, and the Qur'án, unraveled. Through the concerted efforts of all the friends the Standard of Unity must needs be unfurled in those states, and the divine teachings promoted, so that these states may also receive their portion of the heavenly bestowals and a share of the Most Great Guidance. Likewise in the provinces of Canada, such as Newfoundland, Prince Edward Island, Nova Scotia, New Brunswick, Quebec, Ontario, Manitoba, Saskatchewan, Alberta, British Columbia, Ungava, Keewatin, Mackenzie, Yukon, and the Franklin Islands in the Arctic Circle—the believers of God must become self-sacrificing and like unto the candles of guidance become ignited in the provinces of Canada. Should they show forth such a magnanimity, it is assured that they will obtain universal divine confirmations, the heavenly cohorts will reinforce them uninterruptedly, and a most great victory will be obtained. God willing, the call of the Kingdom may reach the ears of the Eskimos, the inhabitants of the Islands of Franklin in the north of Canada, as well as Greenland. Should the fire of the love of God be kindled in Greenland, all the ice of that country will be melted, and its cold weather become temperate—that is, if the hearts be touched with the heat of the love of God, that territory will become a divine rose garden and a heavenly paradise, and the souls, even as fruitful trees, will acquire the utmost freshness and beauty. Effort, the utmost effort, is required. Should you display an effort, so that the fragrances of God may be diffused among the Eskimos, its effect will be very great and far-reaching.

Upon you be greeting and praise!`},

	{"AB00218", `O ye blessed souls:

I desire for you eternal success and prosperity and beg perfect confirmation for each one in the divine world. My hope for you is that each one may shine forth like unto the morning star from the horizon of the world and in this Garden of God become a blessed tree, producing everlasting fruits and results.

Therefore I direct you to that which is conducive to your heavenly confirmation and illumination in the Kingdom of God!

It is this: Alaska is a vast country; although one of the maidservants of the Merciful has hastened to those parts, serving as a librarian in the public library, and according to her ability is not failing in teaching the Cause, yet the call of the Kingdom of God is not yet raised through that spacious territory.

His Holiness Christ says: "Travel ye to the East and to the West of the world and summon the people to the Kingdom of God." Hence the mercy of God must encompass all humanity. Therefore do ye not think it permissible to leave that region deprived of the breezes of the Morn of Guidance. Consequently, strive as far as ye are able to send to those parts fluent speakers, who are detached from aught else save God, attracted with the fragrances of God, and sanctified and purified from all desires and temptations. Their sustenance and food must consist of the teachings of God. First they must themselves live in accordance with those principles, then guide the people. Perchance, God willing, the lights of the Most Great Guidance will illuminate that country, and the breezes of the rose garden of the love of God will perfume the nostrils of the inhabitants of Alaska. Should you be aided to render such a service, rest ye assured that your heads shall be crowned with the diadem of everlasting sovereignty, and at the threshold of oneness you will become the favored and accepted servants.

Likewise the republic of Mexico is very important. The majority of the inhabitants of that country are devoted Catholics. They are totally unaware of the reality of the Bible, the Gospel and the new divine teachings.

O Thou Incomparable God! O Thou Lord of the Kingdom! These souls are Thy heavenly army. Assist them and, with the cohorts of the Supreme Concourse, make them victorious, so that each one of them may become like unto a regiment and conquer these countries through the love of God and the illumination of divine teachings.`},

	{"AB00049", `O ye real Bahá'ís of America:

Praise be to His Highness the Desired One that ye have become confirmed in the promotion of divine teachings in that vast Continent, raised the call of the Kingdom of God in that region and announced the glad tidings of the manifestation of the Lord of Hosts and His Highness the Promised One. Thanks be unto the Lord that ye have become assisted and confirmed in this aim. This is purely through the confirmations of the Lord of Hosts and the breaths of the Holy Spirit. The full measure of your success is as yet unrevealed, its significance still unapprehended. Erelong ye will, with your own eyes, witness how brilliantly every one of you, even as a shining star, will radiate in the firmament of your country the light of divine Guidance, and will bestow upon its people the glory of an everlasting life.

Consider! The station and the confirmation of the apostles in the time of Christ was not known, and no one looked on them with the feeling of importance—nay, rather, they persecuted and ridiculed them. Later on it became evident what crowns studded with the brilliant jewels of guidance were placed on the heads of the apostles, Mary Magdalene and Mary the mother of John.

The range of your future achievements still remains undisclosed. I fervently hope that in the near future the whole earth may be stirred and shaken by the results of your achievements. The hope, therefore, which 'Abdu'l-Bahá cherishes for you is that the same success which has attended your efforts in America may crown your endeavors in other parts of the world, that through you the fame of the Cause of God may be diffused throughout the East and the West, and the advent of the Kingdom of the Lord of Hosts be proclaimed in all the five continents of the globe.

The moment this divine Message is carried forward by the American believers from the shores of America and is propagated through the continents of Europe, of Asia, of Africa and of Australasia, and as far as the islands of the Pacific, this community will find itself securely established upon the throne of an everlasting dominion.

O God, my God! Thou seest me enraptured and attracted toward Thy glorious kingdom, enkindled with the fire of Thy love amongst mankind, a herald of Thy kingdom in these vast and spacious lands, severed from aught else save Thee, relying on Thee, abandoning rest and comfort, remote from my native home, a wanderer in these regions, a stranger fallen upon the ground, humble before Thine exalted threshold, submissive toward the heaven of Thine omnipotent glory, supplicating Thee in the dead of night and at the break of dawn, entreating and invoking Thee at morn and at eventide to graciously aid me to serve Thy Cause.`},

	{"AB00032", `O ye Apostles of Bahá'u'lláh! May my life be sacrificed for you!

The blessed Person of the Promised One is interpreted in the Holy Book as the Lord of Hosts—the heavenly armies. By heavenly armies those souls are intended who are entirely freed from the human world, transformed into celestial spirits and have become divine angels. Such souls are the rays of the Sun of Reality who will illumine all the continents. Each one is holding in his hand a trumpet, blowing the breath of life over all the regions. They are delivered from human qualities and the defects of the world of nature, are characterized with the characteristics of God, and are attracted with the fragrances of the Merciful. Like unto the apostles of Christ, who were filled with Him, these souls also have become filled with His Holiness Bahá'u'lláh; that is, the love of Bahá'u'lláh has so mastered every organ, part and limb of their bodies, as to leave no effect from the promptings of the human world.

These souls are the armies of God and the conquerors of the East and the West. Should one of them turn his face toward some direction and summon the people to the Kingdom of God, all the ideal forces and lordly confirmations will rush to his support and reinforcement. He will behold all the doors open and all the strong fortifications and impregnable castles razed to the ground. Singly and alone he will attack the armies of the world, defeat the right and left wings of the hosts of all the countries, break through the lines of the legions of all the nations and carry his attack to the very center of the powers of the earth. This is the meaning of the Hosts of God.

Any soul from among the believers of Bahá'u'lláh who attains to this station will become known as the Apostle of Bahá'u'lláh. Therefore strive ye with heart and soul so that ye may reach this lofty and exalted position.

O God, my God! Thou seest how black darkness is enshrouding all regions, how all countries are burning with the flame of dissension, and the fire of war and carnage is blazing throughout the East and the West. Blood is flowing, corpses bestrew the ground, and severed heads are fallen on the dust of the battlefield.`},

	{"AB00241", `O ye real friends:

All countries, in the estimation of the one true God, are but one country, and all cities and villages are on an equal footing. Neither holds distinction over another. All of them are the fields of God and the habitation of the souls of men. Through faith and certitude, and the precedence achieved by one over another, however, the dweller conferreth honor upon the dwelling, some of the countries achieve distinction, and attain a preeminent position. For instance, notwithstanding that some of the countries of Europe and of America are distinguished by, and surpass other countries in, the salubrity of their climate, the wholesomeness of their water, and the charm of their mountains, plains and prairies, yet Palestine became the glory of all nations inasmuch as all the holy and divine Manifestations, from the time of Abraham until the appearance of the Seal of the Prophets (Muḥammad), have lived in, or migrated to, or traveled through, that country. Likewise, Mecca and Medina have achieved illimitable glory, as the light of Prophethood shone forth therein. For this reason Palestine and Ḥijáz have been distinguished from all other countries.

Likewise, the continent of America is, in the eyes of the one true God, the land wherein the splendors of His light shall be revealed, where the mysteries of His Faith shall be unveiled, where the righteous will abide and the free assemble. Therefore, every section thereof is blessed: but because these nine states have been favored in faith and assurance, hence through this precedence they have obtained spiritual privilege.

O Thou kind Lord! Praise be unto Thee that Thou hast shown us the highway of guidance, opened the doors of the kingdom and manifested Thyself through the Sun of Reality. To the blind Thou hast given sight; to the deaf Thou hast granted hearing; Thou hast resuscitated the dead; Thou hast enriched the poor; Thou hast shown the way to those who have gone astray; Thou hast led those with parched lips to the fountain of guidance; Thou hast suffered the thirsty fish to reach the ocean of reality; and Thou hast invited the wandering birds to the rose garden of grace.`},

	{"AB00209", `O ye blessed, respected souls:

The philosophers of the ancients, the thinkers of the Middle Ages and the scientists of this and the former centuries have all agreed upon the fact that the best and the most ideal region for the habitation of man is the temperate zone, for in this belt the intellects and thoughts rise to the highest stage of maturity, and the capability and ability of civilization manifest themselves in full efflorescence. When you read history critically and with a penetrating eye, it becomes evident that the majority of the famous men have been born, reared and have done their work in the temperate zone, while very, very few have appeared from the torrid and frigid zones.

Now these sixteen Southern States of the United States are situated in the temperate zone, and in these regions the perfections of the world of nature have been fully revealed. For the moderation of the weather, the beauty of the scenery and the geographical configuration of the country display a great effect in the world of minds and thoughts. This fact is well demonstrated through observation and experience.

Even the holy, divine Manifestations have had a nature in the utmost equilibrium, the health and wholesomeness of their bodies most perfect, their constitutions endowed with physical vigor, their powers functioning in perfect order, and the outward sensations linked with the inward perceptions, working together with extraordinary momentum and coordination.

Therefore in these sixteen states, because they are contiguous to other states and their climate being in the utmost of moderation, unquestionably the divine teachings must reveal themselves with a brighter effulgence, the breaths of the Holy Spirit must display a penetrating intensity, the ocean of the love of God must be stirred with higher waves, the breezes of the rose garden of the divine love be wafted with higher velocity, and the fragrances of holiness be diffused with swiftness and rapidity.

O My God! O my God! Thou seest me in my lowliness and weakness, occupied with the greatest undertaking, determined to raise Thy word among the masses and to spread Thy teachings among Thy peoples.`},

	{"AB00184", `O ye old believers and intimate friends:

God says in the great Qur'án: "He specializes for His Mercy whomsoever He willeth."

These twelve Central States of the United States are like unto the heart of America, and the heart is connected with all the organs and parts of man. If the heart is strengthened, all the organs of the body are reinforced, and if the heart is weak all the physical elements are subjected to feebleness.

Now praise be to God that Chicago and its environs from the beginning of the diffusion of the fragrances of God have been a strong heart. Therefore, through divine bounty and providence it has become confirmed in certain great matters.

First: The call of the Kingdom was in the very beginning raised from Chicago. This is indeed a great privilege, for in future centuries and cycles, it will be as an axis around which the honor of Chicago will revolve.

Second: A number of souls with the utmost firmness and steadfastness arose in that blessed spot in the promotion of the Word of God and even to the present moment, having purified and sanctified the heart from every thought, they are occupied with the promulgation of the teachings of God. Hence the call of praise is raised uninterruptedly from the Supreme Concourse.

Third: During the American journey 'Abdu'l-Bahá several times passed through Chicago and associated with the friends of God. For some time he sojourned in that city. Day and night he was occupied with the mention of the True One and summoned the people to the Kingdom of God.

O Lord, my God! Praise and thanksgiving be unto Thee for Thou hast guided me to the highway of the kingdom, suffered me to walk in this straight and far-stretching path, illumined my eye by beholding the splendors of Thy light, inclined my ear to the melodies of the birds of holiness from the kingdom of mysteries and attracted my heart with Thy love among the righteous.`},

	{"AB00210", `O ye friends and the maidservants of the Merciful, the chosen ones of the Kingdom:

The blessed state of California bears the utmost similarity to the Holy Land, that is, the country of Palestine. The air is of the utmost temperance, the plain very spacious, and the fruits of Palestine are seen in that state in the utmost of freshness and delicacy. When 'Abdu'l-Bahá was traveling and journeying through those states, he found himself in Palestine, for from every standpoint there was a perfect likeness between this region and that state. Even the shores of the Pacific Ocean, in some instances, show perfect resemblance to the shores of the Holy Land—even the flora of the Holy Land have grown on those shores—the study of which had led to much speculation and wonder.

Likewise, in the state of California and other Western states, wonderful scenes of the world of nature, which bewilder the minds of men, are manifest. Lofty mountains, deep canyons, great and majestic waterfalls, and giant trees are witnessed on all sides, while its soil is in the utmost fertility and richness.

Now California and the other Western States must earn an ideal similarity with the Holy Land, and from that state and that region the breaths of the Holy Spirit be diffused to all parts of America and Europe, that the call of the Kingdom of God may exhilarate and rejoice all the ears, the divine principles bestow a new life, the different parties may become one party, the divergent ideas may disappear and revolve around one unique center.

O God! O God! This is a broken-winged bird and his flight is very slow—assist him so that he may fly toward the apex of prosperity and salvation, wing his way with the utmost joy and happiness throughout the illimitable space, raise his melody in Thy Supreme Name in all the regions, exhilarate the ears with this call, and brighten the eyes by beholding the signs of guidance.`},

	{"AB00169", `O ye kind friends and the maidservants of the Merciful:

In the great Qur'án, God says: "Thou shalt see no difference in the creatures of God." In other words, He says: From the ideal standpoint, there is no variation between the creatures of God, because they are all created by Him. From the above premise, a conclusion is drawn, that there is no difference between countries. The future of the Dominion of Canada, however, is very great, and the events connected with it infinitely glorious. It shall become the object of the glance of Providence, and shall show forth the bounties of the All-Glorious.

'Abdu'l-Bahá during his journey and sojourn through that Dominion obtained the utmost joy. Before My departure, many souls warned Me not to travel to Montreal, saying, the majority of the inhabitants are Catholics, and are in the utmost fanaticism, that they are submerged in the sea of imitations, that they have not the capability to hearken to the call of the Kingdom of God, that the veil of bigotry has so covered the eyes that they have deprived themselves from beholding the signs of the Most Great Guidance, and that the dogmas have taken possession of the hearts entirely, leaving no trace of reality.

O ye believers of God! Be not concerned with the smallness of your numbers, neither be oppressed by the multitude of an unbelieving world. Five grains of wheat will be endued with heavenly blessing, whereas a thousand tons of tares will yield no results or effect. One fruitful tree will be conducive to the life of society, whereas a thousand forests of wild trees offer no fruits. The plain is covered with pebbles, but precious stones are rare. One pearl is better than a thousand wildernesses of sand, especially this pearl of great price, which is endowed with divine blessing.

Praise be to Thee, O my God! These are Thy servants who are attracted by the fragrances of Thy mercifulness, are enkindled by the fire burning in the tree of Thy singleness, and whose eyes are brightened by beholding the splendors of the light shining in the Sinai of Thy oneness.

O God, my God! Thou beholdest this weak one begging for celestial strength, this poor one craving Thy heavenly treasures, this thirsty one longing for the fountain of eternal life, this afflicted one yearning for Thy promised healing through Thy boundless mercy which Thou hast destined for Thy chosen servants in Thy kingdom on high.`},

	{"AB00094", `O ye heavenly souls, sons and daughters of the Kingdom:

God says in the Qur'án: "Take ye hold of the Cord of God, all of you, and become ye not disunited."

In the contingent world there are many collective centers which are conducive to association and unity between the children of men. For example, patriotism is a collective center; nationalism is a collective center; identity of interests is a collective center; political alliance is a collective center; the union of ideals is a collective center, and the prosperity of the world of humanity is dependent upon the organization and promotion of the collective centers. Nevertheless, all the above institutions are, in reality, the matter and not the substance, accidental and not eternal—temporary and not everlasting. With the appearance of great revolutions and upheavals, all these collective centers are swept away. But the Collective Center of the Kingdom, embodying the institutions and divine teachings, is the eternal Collective Center. It establishes relationship between the East and the West, organizes the oneness of the world of humanity, and destroys the foundation of differences. It overcomes and includes all the other collective centers. Like unto the ray of the sun, it dispels entirely the darkness encompassing all the regions, bestows ideal life, and causes the effulgence of divine illumination.

Consider! The people of the East and the West were in the utmost strangeness. Now to what a high degree they are acquainted with each other and united together! How far are the inhabitants of Persia from the remotest countries of America! And now observe how great has been the influence of the heavenly power, for the distance of thousands of miles has become identical with one step!

O God! O God! Thou seest my weakness, lowliness and humility before Thy creatures; nevertheless, I have trusted in Thee and have arisen in the promotion of Thy teachings among Thy strong servants, relying on Thy power and might.`},
}

func sqlEsc(s string) string {
	return strings.NewReplacer(`\`, `\\`, `'`, `''`).Replace(s)
}

func splitParts(text string, size int) []string {
	var parts []string
	for len(text) > size {
		// Split at a space boundary
		cut := size
		for cut > size-100 && cut > 0 && text[cut] != ' ' {
			cut--
		}
		if cut == 0 {
			cut = size
		}
		parts = append(parts, text[:cut])
		text = strings.TrimSpace(text[cut:])
	}
	if len(text) > 0 {
		parts = append(parts, text)
	}
	return parts
}

func execSQL(sql string) {
	cmd := exec.Command("dolt", "sql", "-q", sql)
	cmd.Dir = doltDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		fmt.Fprintf(os.Stderr, "[dolt exec error] %v: %s\n", err, string(out))
	}
}

func main() {
	dryRun := flag.Bool("dry-run", false, "Print SQL but don't execute")
	flag.Parse()

	total := 0
	for _, t := range tablets {
		parts := splitParts(t.text, partSize)
		for i, part := range parts {
			sql := fmt.Sprintf(
				"INSERT INTO inventory_fulltext (phelps, language, part, text, source) VALUES ('%s', 'en', %d, '%s', 'bahai.org') ON DUPLICATE KEY UPDATE text=VALUES(text), source=VALUES(source);",
				t.pin, i, sqlEsc(part),
			)
			if *dryRun {
				fmt.Printf("-- %s part %d (%d chars)\n%s\n\n", t.pin, i, len(part), sql)
			} else {
				execSQL(sql)
				fmt.Printf("  %s part %d (%d chars)\n", t.pin, i, len(part))
			}
			total++
		}
	}
	fmt.Fprintf(os.Stderr, "Done: %d parts inserted for %d tablets\n", total, len(tablets))
}
