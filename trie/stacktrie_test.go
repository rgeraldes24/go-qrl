// Copyright 2020 The go-ethereum Authors
// This file is part of the go-ethereum library.
//
// The go-ethereum library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// The go-ethereum library is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Lesser General Public License for more details.
//
// You should have received a copy of the GNU Lesser General Public License
// along with the go-ethereum library. If not, see <http://www.gnu.org/licenses/>.

package trie

import (
	"bytes"
	"math/big"
	"testing"

	"github.com/theQRL/go-qrl/common"
	"github.com/theQRL/go-qrl/core/rawdb"
	"github.com/theQRL/go-qrl/crypto"
)

func TestStackTrieInsertAndHash(t *testing.T) {
	type KeyValueHash struct {
		K string // Hex string for key.
		V string // Value, directly converted to bytes.
		H string // Expected root hash after insert of (K, V) to an existing trie.
	}
	tests := [][]KeyValueHash{
		{ // {0:0, 7:0, f:0}
			{"00", "v_______________________0___0", "a120bdc511e0de92237fbf3aa164b05f84941f544203522c5ff5f69a5b4b7458cae0c673f3b25584034314018834e915355c3932f1cd4ede57b8a1b67273f599"},
			{"70", "v_______________________0___1", "f5ffaf3a55e31f56a0d932e080c2c3109091fd462a4660422880ab0b9f07c1e01ef0b61b96f5b11fa5399f2ef5477db1c83fe87a842d8f95c9fb5991ba78f46d"},
			{"f0", "v_______________________0___2", "4f32a33cf6e303655d3ba205cd0a68499c9262f1e408311d4378a7a789073997c296921091e50690496b1d1c08829d2b022c0d4e0bc3a34e1751d100766bcc03"},
		},
		{ // {1:0cc, e:{1:fc, e:fc}}
			{"10cc", "v_______________________1___0", "ecebf840b354b67ea7f1fbf1a0e880794a41668f1c8c1295a80b5f52027c67dea9d3b8a54727e9c3e922a893e94b1246bba062d0c3819ba833e7fa1ff402daf5"},
			{"e1fc", "v_______________________1___1", "d57e3d43f129899b80558330295f7d2ab8380bd95086c2ad001c884a32e8ae487a9e5c142f6fdd4a865d81052953e7c13d648ad1b36612cfe612fa93c958c7d5"},
			{"eefc", "v_______________________1___2", "2aab6a87ea128b2884decab0cfe8e69802075221374457b27873d22c31754d547ed86b274bafa4379c6b83e94c8be2f2526e18c676368a5420b50a2a853d98b2"},
		},
		{ // {b:{a:ac, b:ac}, d:acc}
			{"baac", "v_______________________2___0", "6f3a16ac7114e7971987f8fd741cbd00ae55af3cf0aa3adf17b55b4519051ace8aa950bb093fda462fc0d180e981c0d99316fe6fc9013ec1183367b983bdb5ca"},
			{"bbac", "v_______________________2___1", "f09db0c78b747108bc24b512e0660a01330a1e98a6ad4921a44aae4425abeb4a0d5f68f72ba62d2afbbe07b52d343a9e5b92290e81a1ca3fdc7f7aa1dcc44239"},
			{"dacc", "v_______________________2___2", "b3a82e82ae33189aab1da963d8b2808cc1f049c8821b13a0f43c7bdf71e7a68741f009c3cc48194ac7efd461c9657ea3a35094dcd6d44970febdf788324fb77c"},
		},
		{ // {0:0cccc, 2:456{0:0, 2:2}
			{"00cccc", "v_______________________3___0", "c066c6c0655f2a0f943d1423a7e0eddf1a76db6fe6336fa231c54efc6f1e524c645a71a2cc837dc119e3b2e32b2862ab35fcc34445a9a45a30336f04ec7493e4"},
			{"245600", "v_______________________3___1", "c473cacd286a324603293eb469175ba2d2c3e35420a70b85023a9bd5e3e950f1aba72cbb438b534b5e9c89c735463fcd48955478491ab2e8c5c661a689501b42"},
			{"245622", "v_______________________3___2", "4c3a5b61a8c29574046d8ef780eaa662a38304b12e35b62570b351ae1a7aa51025b2fdafdab986ad6d3b7c85cccece16f443b535d021e5b2d0cb5281fc60bbac"},
		},
		{ // {1:4567{1:1c, 3:3c}, 3:0cccccc}
			{"1456711c", "v_______________________4___0", "057b2b11df4c11047797611a42816f88c4471f234a814a333c9cbbc437318677f1cc13185a7ba162e099a036457acfa3c57d3b1322fb5ccddefc999ab239537d"},
			{"1456733c", "v_______________________4___1", "ab8b5a327e1880717672d60a4fd1c7bcada7d89b572c4b6fb20782e0de0a752b71298bade0e8e5b3dc0ff118f5207c518956dac695996a3f9f99afa35f0c2e94"},
			{"30cccccc", "v_______________________4___2", "664b0f982d43803d1eeff6d68f4555f67efe0f17634b0f00bb8a8267bf87ab4e8b07a60bdb9cd28430adb5c88a82f093ca8ec476c2bd912c561c1a42d959a22d"},
		},
		{ // 8800{1:f, 2:e, 3:d}
			{"88001f", "v_______________________5___0", "0af6713b155b6368601dd57566aea04d6336b92bb5d6e4be426bb6e2eb60bea3fb170878e51a16ca117e604a7bb69595649d94d3117d909f0b3bd568a4703872"},
			{"88002e", "v_______________________5___1", "79f3dbba26d398984e8e0e132f6105bf4e229475e11005c698d29df762107296e2ad1c31668e424b21aba12775d5b5a5eb98074d1f5ec651d79d77d58e2492a3"},
			{"88003d", "v_______________________5___2", "79b145ecaafc8abaf98e9b50efd4278d97fde7f9d28a13650abbd9b1df47b35cdfbb56295d378854ecde2fe0a42c05152a78a9d981c6edef8672e208a42c68a4"},
		},
		{ // 0{1:fc, 2:ec, 4:dc}
			{"01fc", "v_______________________6___0", "315c3dcabb1c55c6968ce3d85ca639827e8e5c3072027cf1a46f00b43de2ab4129cd655a416ee88b43b67d6f0d971c1aa454d9df58f25be16df363dce4d203cf"},
			{"02ec", "v_______________________6___1", "3f65d90d6a437f461aebfc942c064b4ca21ff614394877fb929e159368b022bfb2b4fe2587f94bfc8c219dbe86a214daca4637f4e16ae8070240f450f0c62249"},
			{"04dc", "v_______________________6___2", "6a33cab974ce3e08bb6fa213cea06bd75320b50c2fffa7b259d5f1f054520fd858bf44aae6143925f07581cf7ec803b29268f0d2b11f5c74f32e70258bff70c9"},
		},
		{ // f{0:fccc, f:ff{0:f, f:f}}
			{"f0fccc", "v_______________________7___0", "1d8d9ce3fb398f37ba9d7ea7d983c14f373e80ac902e718f405506730dbf965ea7891dc21db6ab65c7106744327c8820f53bd22ff5f7ebde84dada6932644d90"},
			{"ffff0f", "v_______________________7___1", "4f620e7281bbbd864856d669b536f45dfcf5333e20ed8924ef0959f0689fda06020ab87bf702623f318e2972b95a988ea6da71573247a26f54c9b54e231fd086"},
			{"ffffff", "v_______________________7___2", "592cb3950c820a8ae77abfc1795c44c535c5c5a503f16cf508269078a38d016e98ceb7ff8a4f497acf5817caa409a962420217893ad615d89085e563dfb60745"},
		},
		{ // ff{0:f{0:f, f:f}, f:fcc}
			{"ff0f0f", "v_______________________8___0", "8ac1893361dcc7f39c002dc79f784e649f7325195094ebc5b71e536e84e4f8748e357a9452cbea690d2ad7d6cedef600b41784a859b204fd86a7342183767d40"},
			{"ff0fff", "v_______________________8___1", "75a6adec8eabdf586d52673832a96c281df5dd392357220d26cef399c80411031c23016975b3008a0497d1d36e54712d51a96444dd9d0a45fc9154a6d62f8baf"},
			{"ffffcc", "v_______________________8___2", "7123dcee17403a111205a852154ced264d28e44220608c93ae79af3a9b4774527400079dbf8a11a18a71da60fc290246d7395ee0f43347cc4a866983f8dd4e69"},
		},
		{
			{"123d", "x___________________________0", "333fa110803d48f299ab8cf11198d1f0b01a1ebfcd22a4b1f8c53fbf1f96451514d02717df262c9cc85b9bd0db57af40a69bbb8a66de7d27fb9d2619b9c6151a"},
			{"123e", "x___________________________1", "42e108819cf029a750585e16700a4a94df08eff2e7b5d51db4465e4510c9c2a312d4228cda63167b13f5e00312dd40f05686e4e097a1eb99e8167541667938ed"},
			{"123f", "x___________________________2", "e480078e85e17fbfa815aa392c3599e0daa93c2c0db2e75dd56e9dbb73441d811d548df621d1e7b4d51b22123faf50d85cb3bae781d10e58c7cb2277ea67aecd"},
		},
		{
			{"123d", "x___________________________0", "333fa110803d48f299ab8cf11198d1f0b01a1ebfcd22a4b1f8c53fbf1f96451514d02717df262c9cc85b9bd0db57af40a69bbb8a66de7d27fb9d2619b9c6151a"},
			{"123e", "x___________________________1", "42e108819cf029a750585e16700a4a94df08eff2e7b5d51db4465e4510c9c2a312d4228cda63167b13f5e00312dd40f05686e4e097a1eb99e8167541667938ed"},
			{"124a", "x___________________________2", "e0cd291918817174175d71d763067eb6a7eb62d280b8ccbb1b0a32fc03d3e31d8d1e76c5ced0e626e48e1a14c6523827946e7e25ba5511d9c065d12ad9563597"},
		},
		{
			{"123d", "x___________________________0", "333fa110803d48f299ab8cf11198d1f0b01a1ebfcd22a4b1f8c53fbf1f96451514d02717df262c9cc85b9bd0db57af40a69bbb8a66de7d27fb9d2619b9c6151a"},
			{"123e", "x___________________________1", "42e108819cf029a750585e16700a4a94df08eff2e7b5d51db4465e4510c9c2a312d4228cda63167b13f5e00312dd40f05686e4e097a1eb99e8167541667938ed"},
			{"13aa", "x___________________________2", "8f5ef9a5df467b674989c28baebee1ef4584ec12e38af58f193e7219b80a1b59f065da1b2cb982ad5b1fbf675d2815a13dec194bb878ec2ee636fb4753ea3b53"},
		},
		{
			{"123d", "x___________________________0", "333fa110803d48f299ab8cf11198d1f0b01a1ebfcd22a4b1f8c53fbf1f96451514d02717df262c9cc85b9bd0db57af40a69bbb8a66de7d27fb9d2619b9c6151a"},
			{"123e", "x___________________________1", "42e108819cf029a750585e16700a4a94df08eff2e7b5d51db4465e4510c9c2a312d4228cda63167b13f5e00312dd40f05686e4e097a1eb99e8167541667938ed"},
			{"2aaa", "x___________________________2", "1de4c5baea1d96102ad17d9af5c44cd615e9729aeb4f35cf4285f023373d5ce3a7613708cd1c001cea326b43449af162e6f140fc76b12918b339c5c3bd2dd739"},
		},
		{
			{"1234da", "x___________________________0", "f2a9ed3ca0426031c7f05a1275f775c0b76f717ac8ee264b734c9936a76d30974c37f115996854677a940554470655a1e407571a32d07c93cf5557492bf71ac7"},
			{"1234ea", "x___________________________1", "8cd90c08b160f2e3d59833b8e64fef34dbe64a36fd3989348b1af8d25e77d6641a024fda7874c2be75d43d0bc50b329e1ad03108a7bafc560cb76cf97736a415"},
			{"1234fa", "x___________________________2", "2c8fabad71e74b9c46af233eb5a9120655321387d9c16e10951561838d239ecb770220506e696b9df924e1735a962c960f189ee0324bfcab07fb3e455f7d4adc"},
		},
		{
			{"1234da", "x___________________________0", "f2a9ed3ca0426031c7f05a1275f775c0b76f717ac8ee264b734c9936a76d30974c37f115996854677a940554470655a1e407571a32d07c93cf5557492bf71ac7"},
			{"1234ea", "x___________________________1", "8cd90c08b160f2e3d59833b8e64fef34dbe64a36fd3989348b1af8d25e77d6641a024fda7874c2be75d43d0bc50b329e1ad03108a7bafc560cb76cf97736a415"},
			{"1235aa", "x___________________________2", "6166ba9606223840a1df471c665025e2c9ae8c473bd909c07cd074418b9d1f940cdc3b91fe786ae80484c94a1df98c0a97e9837475b5238b8292fb1f0b2b43c3"},
		},
		{
			{"1234da", "x___________________________0", "f2a9ed3ca0426031c7f05a1275f775c0b76f717ac8ee264b734c9936a76d30974c37f115996854677a940554470655a1e407571a32d07c93cf5557492bf71ac7"},
			{"1234ea", "x___________________________1", "8cd90c08b160f2e3d59833b8e64fef34dbe64a36fd3989348b1af8d25e77d6641a024fda7874c2be75d43d0bc50b329e1ad03108a7bafc560cb76cf97736a415"},
			{"124aaa", "x___________________________2", "4032851625891d82ae1e1588829e55c56dd2cb87abbf64c07e6306faef90bcfbc5213b3a42eaa2991ae4bb9958ce10c90bf42ddbd4950337eafb8f65c27a3621"},
		},
		{
			{"1234da", "x___________________________0", "f2a9ed3ca0426031c7f05a1275f775c0b76f717ac8ee264b734c9936a76d30974c37f115996854677a940554470655a1e407571a32d07c93cf5557492bf71ac7"},
			{"1234ea", "x___________________________1", "8cd90c08b160f2e3d59833b8e64fef34dbe64a36fd3989348b1af8d25e77d6641a024fda7874c2be75d43d0bc50b329e1ad03108a7bafc560cb76cf97736a415"},
			{"13aaaa", "x___________________________2", "2703552c216122697bd827180f45fc2c72ca9337b2626e4ca192010afe241c10b15b4b23c4d94422e4bd8fbd86857f8ab00433990c10b799daa45d224520a40e"},
		},
		{
			{"1234da", "x___________________________0", "f2a9ed3ca0426031c7f05a1275f775c0b76f717ac8ee264b734c9936a76d30974c37f115996854677a940554470655a1e407571a32d07c93cf5557492bf71ac7"},
			{"1234ea", "x___________________________1", "8cd90c08b160f2e3d59833b8e64fef34dbe64a36fd3989348b1af8d25e77d6641a024fda7874c2be75d43d0bc50b329e1ad03108a7bafc560cb76cf97736a415"},
			{"2aaaaa", "x___________________________2", "7061b7ed1a52aec6a76e1072f4c63593625ec00a81fa71649dd4f72eda3e19b1f4aaeb06bdfce22eecda0cc4dca3c45b5ccab92f90ca0b6054ba72b36a398165"},
		},
		{
			{"000000", "x___________________________0", "3c91361a404884976963fd3a0552cef8242236815947e32d2ac087ac2946de0524e1d2bb7112980cb75e482009f54f011f71b281933053fcafe3fe311566a7fa"},
			{"1234da", "x___________________________1", "9defedd371c3ab3456ea3d5a4a40293ba065eaace1da263adaeac93d26d5c3d4ac2db4adb4d4603d78884a2fc9af2fc1c697ecfc4ef014a9ff976a386c8ef15d"},
			{"1234ea", "x___________________________2", "2a2297086137a3d9a0424d2384b26c977edad2b8fc5eea78b72156ccd9340f4235f40e5a6262ceb97c24ad3e0b9c7f5edea8e52b2417c2dcff4bf8d4f24e9ac5"},
			{"1234fa", "x___________________________3", "8a8f7ae16fbd93c22f980581ec99747f337ef373d76be4dbd6b121f002e1b6ceec5959cfb38b799c2c281a9019f77279606e2ae1c0784fb02a05ef1bc232bac4"},
		},
		{
			{"000000", "x___________________________0", "3c91361a404884976963fd3a0552cef8242236815947e32d2ac087ac2946de0524e1d2bb7112980cb75e482009f54f011f71b281933053fcafe3fe311566a7fa"},
			{"1234da", "x___________________________1", "9defedd371c3ab3456ea3d5a4a40293ba065eaace1da263adaeac93d26d5c3d4ac2db4adb4d4603d78884a2fc9af2fc1c697ecfc4ef014a9ff976a386c8ef15d"},
			{"1234ea", "x___________________________2", "2a2297086137a3d9a0424d2384b26c977edad2b8fc5eea78b72156ccd9340f4235f40e5a6262ceb97c24ad3e0b9c7f5edea8e52b2417c2dcff4bf8d4f24e9ac5"},
			{"1235aa", "x___________________________3", "f96685bcd5291bd9b4c27278f81b2ed386fc2633a08c999d0c47d0f8d1f5235a3466468ce521edeb50ef709a4bc690de83b5a197762b00dde7f5da9e73678296"},
		},
		{
			{"000000", "x___________________________0", "3c91361a404884976963fd3a0552cef8242236815947e32d2ac087ac2946de0524e1d2bb7112980cb75e482009f54f011f71b281933053fcafe3fe311566a7fa"},
			{"1234da", "x___________________________1", "9defedd371c3ab3456ea3d5a4a40293ba065eaace1da263adaeac93d26d5c3d4ac2db4adb4d4603d78884a2fc9af2fc1c697ecfc4ef014a9ff976a386c8ef15d"},
			{"1234ea", "x___________________________2", "2a2297086137a3d9a0424d2384b26c977edad2b8fc5eea78b72156ccd9340f4235f40e5a6262ceb97c24ad3e0b9c7f5edea8e52b2417c2dcff4bf8d4f24e9ac5"},
			{"124aaa", "x___________________________3", "9e5fd2d5f8a05fd434d97160eee5796878cc6c8fe17319da3abf3ced27c71ffa9a7b2d37092db466203165a6257de894a12f3f340002af5e224840b6be0ffaec"},
		},
		{
			{"000000", "x___________________________0", "3c91361a404884976963fd3a0552cef8242236815947e32d2ac087ac2946de0524e1d2bb7112980cb75e482009f54f011f71b281933053fcafe3fe311566a7fa"},
			{"1234da", "x___________________________1", "9defedd371c3ab3456ea3d5a4a40293ba065eaace1da263adaeac93d26d5c3d4ac2db4adb4d4603d78884a2fc9af2fc1c697ecfc4ef014a9ff976a386c8ef15d"},
			{"1234ea", "x___________________________2", "2a2297086137a3d9a0424d2384b26c977edad2b8fc5eea78b72156ccd9340f4235f40e5a6262ceb97c24ad3e0b9c7f5edea8e52b2417c2dcff4bf8d4f24e9ac5"},
			{"13aaaa", "x___________________________3", "0f0d1f5fe618e3d6f8f1c516bae64c300711c63b009c0ef8a3de7eeaa7c828033eb351b7ceee8e78b62f018b8ae9f04db4b9dc2cf2886aaa158bfbbef1fefe02"},
		},
		{
			{"0000", "x___________________________0", "dbe6c695adb71f6ab4a09ea0bdeaa7311dd7a6c18f51c9e04cd59d514b88ebde856ffd77783698b8a4ef5f9c825be7a60e6d4dbdb06d9d33771c81012452cdab"},
			{"123d", "x___________________________1", "cb9097b805356ed382c437afa72b0ed23a96428e87857caf7b13bf8a086da019e3ef3cdcab9e0669c2637a4486df6dbe682e2f7f7be0be84055af9067f1d1e44"},
			{"123e", "x___________________________2", "d7a59eb2513ff9a80c7f24d2c941a41a68b387be5c58dc72b93e9ea5b3165dbe0eba7466158e56b83d3d3c78162f2e05c283fc6a6159b30536bb5869beead46e"},
			{"123f", "x___________________________3", "03ff0ba9d4cc1ea45b96ccb1a54a5343b1bff78980a82d6ffe9c1f25162ad11a98564ef4f60c45196bc7a439bedddae215dec4e167d24bddb9fe0e392447a052"},
		},
		{
			{"0000", "x___________________________0", "dbe6c695adb71f6ab4a09ea0bdeaa7311dd7a6c18f51c9e04cd59d514b88ebde856ffd77783698b8a4ef5f9c825be7a60e6d4dbdb06d9d33771c81012452cdab"},
			{"123d", "x___________________________1", "cb9097b805356ed382c437afa72b0ed23a96428e87857caf7b13bf8a086da019e3ef3cdcab9e0669c2637a4486df6dbe682e2f7f7be0be84055af9067f1d1e44"},
			{"123e", "x___________________________2", "d7a59eb2513ff9a80c7f24d2c941a41a68b387be5c58dc72b93e9ea5b3165dbe0eba7466158e56b83d3d3c78162f2e05c283fc6a6159b30536bb5869beead46e"},
			{"124a", "x___________________________3", "12d9b1c79e3d8e9aff681679473fda9905b79969dc21fff3f046d7fab822956192863bea62bc6d21a05b008a4316fa79c81c446db70938622991f5dc1bdea7d8"},
		},
		{
			{"0000", "x___________________________0", "dbe6c695adb71f6ab4a09ea0bdeaa7311dd7a6c18f51c9e04cd59d514b88ebde856ffd77783698b8a4ef5f9c825be7a60e6d4dbdb06d9d33771c81012452cdab"},
			{"123d", "x___________________________1", "cb9097b805356ed382c437afa72b0ed23a96428e87857caf7b13bf8a086da019e3ef3cdcab9e0669c2637a4486df6dbe682e2f7f7be0be84055af9067f1d1e44"},
			{"123e", "x___________________________2", "d7a59eb2513ff9a80c7f24d2c941a41a68b387be5c58dc72b93e9ea5b3165dbe0eba7466158e56b83d3d3c78162f2e05c283fc6a6159b30536bb5869beead46e"},
			{"13aa", "x___________________________3", "c7012e2fd000a7636aebdd0d309a57a2a123ec1d8ee4965f7f47b3ca95d285858272bf008b114c6c27e49118fa4763df94e7c54ce330f1e28a2553e37bc8794b"},
		},
		{ // branch node with short values
			{"01", "a", "be2a4aed9c3fee318c3afa1f98dbbaa19defe36dcbb733c048eb6ea16d2c07c768cf102a092c7789ab3a5670f66f5f6c493026f4332ea433efde80d2c063254d"},
			{"80", "b", "7f370911a5ac5e7bc3387994abf4ef22716a96ea1a6d13f320a0edbc6f6e800e73acef3fcde58dcd2cce7eb6998f071c9c478cbb94fe6fb81405ad6740455fc9"},
			{"ee", "c", "cbdc3c0e2679c87d34109b6773069d23f14afd2e874c536c98304948d7c79ed9494fc6b987df1d0a8f8e6cebf5ea3ab927f1f58698071958cbcf28e4a9634629"},
			{"ff", "d", "f6e3c2a624c917cc5dc51b220222d1702d355615589e0abe72f362b6d39bc6dd18e4560f0de6f6b9cae3ce599e510690968d202accc74f796169aba6a5659e0e"},
		},
		{ // ext node with short branch node, then becoming long
			{"a0", "a", "87111e0d7859c38a061991c34242c10c82f59aafb4289f616d253f7068fe535963b86e237d717ac02da472ec86e6c06132a42ae944bbcd75398c7aea1cbcadde"},
			{"a1", "b", "9861990765e3368ff3a24abb78981a526b625eee3957c1001ae5724040642105ebfc82a4a13ca069f1f559bb2f69b3ae970bbc3cea191e095eb5ae1716dc2791"},
			{"a2", "c", "f9a5a6b080666d39ef4d4048d0d579f975071d0dc7cc9a581770dd6d35befd292a298b62ddee6acebbe673c82e54d926a036ee7cb8ff9ffc7357db1573b2110a"},
			{"a3", "d", "a8f810a93c3ecff6762efee47ddace4d415217094923661ec670a740df41d5a804a3c43a94bc92d0407bf90c93b1a7c8c99573bee4396c5edc6545c93c573d6a"},
			{"a4", "e", "a245ac41e8c8d9a368a802df32945e8ca86b08771a75ff1f681b35d62815274061abf63f2c9bcc45abdbded6a779696da0ff5ab6d387905cbc5c7b0ba2c00264"},
			{"a5", "f", "214b455a04f248d13ec6023a0c2000b10c71a125a349ea2150b413b8b37795142ede80b7a46910446296c6c0c78f2416355a29448eac406188b951006c73f015"},
			{"a6", "g", "ee704259dfaa13e5d6907b02c6910e999c930ccd3a8dc91632ff1e0b7907d244353f397880649dbc7c8cabc38df87e7171ebff086ef1b5028b1ad706824f4160"},
		},
		{ // branch node with short values, then long ones
			{"a001", "v1", "bd0eeb1cc2877022c02e8ff0838386b1e8adf35f0a90243c8c24b736e975c3e37aa74a128811fdd982727eaa9cc2c066d412fca434020ebe5b0e5ece1d5e03b4"},
			{"b002", "v2", "54777621d2c650d6ec3c9d50fb654134ec8b83684c6a41489c260c2c32ae7cbe239e5e2ada74ec377ca6c7be5c3b42d52b8037b2e59f80f544670fb67293b02b"},
			{"c003", "v___________________________3", "9bbfaa0b6ba85550f81e52f7a76df532563fc5ec91ca4a0184c2eb86532c1d7be958d13bb54fc498d176ba76518484725b5b39ff071d50db30c1b5a103ce4efe"},
			{"d004", "v___________________________4", "c14b37b94a20ccdda3bef900022bc4b67905bf08bd08f65f51ff906766bf5c9d96895ba64d9e9153e6110f5fc79c9ecf80e006f831c0407a421b84b2aaf3a97e"},
		},
		{ // ext node to branch node with short values, then long ones
			{"8002", "v1", "50d5226d44781314318fce4d6658e02c7f971db7a9783b762b9463c9d69eb62c8574422d37bf2dbafd1a33a920867537799afaa36e9e706ece527603433ef33a"},
			{"8004", "v2", "68f236d55aca1a95f6b30fed195fd561f09c61350f91e14fdeb74de23467a727aa4f9b0f0461d16cd14ddd82adafa6c4763dd16320289f3b278f04ea1db51501"},
			{"8008", "v___________________________3", "d68e6f91f968a05a67d7f95330891abee2e4b84c47ab9b3de10c9e5411d61939eeeaf43cf28527d6bc83b37f39d9d385f91d3c7be0f8ff32b0560721f0dbf97a"},
			{"800d", "v___________________________4", "12fc80b5cb4b7f6be49e894c9cb7bd3bb56784b3522178481dcf6d41f828ca5e12c39cf9278fa1a2a3a5678c15d9f061017ab0fbef89a07e0b876b3fb4d5be12"},
		},
		{ // ext node with a child of size 31 (Y) and branch node with a child of size 31 (X)
			{"000001", "ZZZZZZZZZ", "fc9ca69a59b5235b5b6d512c822975ae4a90e92779dc3d66eded2f6d5585c34a3e87e444688376e809b83d6191610ba99c9f9a146f933a0f56470cbbe7779799"},
			{"000002", "Y", "a16701e831b82f5566eef4fa06d727a4d9e88b02a85757d8308c422d74736418271f31efd89e5c4db94df5c825600ac6505992025ce2d5b31c7f79dd2c15b6b3"},
			{"000003", "XXXXXXXXXXXXXXXXXXXXXXXXXXXX", "ef58096aca42afeea2827c1d1d39fe8ed7b309a78178867cb040bfec0d96a25c0d7e9bc7fcd0a01261a41ef05b85ce72a53fe0bfc330fac8190e2df7a95a113d"},
		},
	}
	st := NewStackTrie(nil)
	for i, test := range tests {
		// The StackTrie does not allow Insert(), Hash(), Insert(), ...
		// so we will create new trie for every sequence length of inserts.
		for l := 1; l <= len(test); l++ {
			st.Reset()
			for j := range l {
				kv := &test[j]
				if err := st.Update(common.FromHex(kv.K), []byte(kv.V)); err != nil {
					t.Fatal(err)
				}
			}
			expected := common.HexToHash512(test[l-1].H)
			if h := st.Hash(); h != expected {
				t.Errorf("%d(%d): root hash mismatch: %x, expected %x", i, l, h, expected)
			}
		}
	}
}

func TestSizeBug(t *testing.T) {
	st := NewStackTrie(nil)
	nt := NewEmpty(NewDatabase(rawdb.NewMemoryDatabase(), nil))

	leaf := common.FromHex("290decd9548b62a8d60345a988386fc84ba6bc95484008f6362f93160ef3e563")
	value := common.FromHex("94cf40d0d2b44f2b66e07cace1372ca42b73cf21a3")

	nt.Update(leaf, value)
	st.Update(leaf, value)

	if nt.Hash() != st.Hash() {
		t.Fatalf("error %x != %x", st.Hash(), nt.Hash())
	}
}

func TestEmptyBug(t *testing.T) {
	st := NewStackTrie(nil)
	nt := NewEmpty(NewDatabase(rawdb.NewMemoryDatabase(), nil))

	//leaf := common.FromHex("290decd9548b62a8d60345a988386fc84ba6bc95484008f6362f93160ef3e563")
	//value := common.FromHex("94cf40d0d2b44f2b66e07cace1372ca42b73cf21a3")
	kvs := []struct {
		K string
		V string
	}{
		{K: "405787fa12a823e0f2b7631cc41b3ba8828b3321ca811111fa75cd3aa3bb5ace", V: "9496f4ec2bf9dab484cac6be589e8417d84781be08"},
		{K: "40edb63a35fcf86c08022722aa3287cdd36440d671b4918131b2514795fefa9c", V: "01"},
		{K: "b10e2d527612073b26eecdfd717e6a320cf44b4afac2b0732d9fcbe2b7fa0cf6", V: "947a30f7736e48d6599356464ba4c150d8da0302ff"},
		{K: "c2575a0e9e593c00f959f8c92f12db2869c3395a3b0502d05e2516446f71f85b", V: "02"},
	}

	for _, kv := range kvs {
		nt.Update(common.FromHex(kv.K), common.FromHex(kv.V))
		st.Update(common.FromHex(kv.K), common.FromHex(kv.V))
	}

	if nt.Hash() != st.Hash() {
		t.Fatalf("error %x != %x", st.Hash(), nt.Hash())
	}
}

func TestValLength56(t *testing.T) {
	st := NewStackTrie(nil)
	nt := NewEmpty(NewDatabase(rawdb.NewMemoryDatabase(), nil))

	//leaf := common.FromHex("290decd9548b62a8d60345a988386fc84ba6bc95484008f6362f93160ef3e563")
	//value := common.FromHex("94cf40d0d2b44f2b66e07cace1372ca42b73cf21a3")
	kvs := []struct {
		K string
		V string
	}{
		{K: "405787fa12a823e0f2b7631cc41b3ba8828b3321ca811111fa75cd3aa3bb5ace", V: "1111111111111111111111111111111111111111111111111111111111111111111111111111111111111111111111111111111111111111"},
	}

	for _, kv := range kvs {
		nt.Update(common.FromHex(kv.K), common.FromHex(kv.V))
		st.Update(common.FromHex(kv.K), common.FromHex(kv.V))
	}

	if nt.Hash() != st.Hash() {
		t.Fatalf("error %x != %x", st.Hash(), nt.Hash())
	}
}

// TestUpdateSmallNodes tests a case where the leaves are small (both key and value),
// which causes a lot of node-within-node. This case was found via fuzzing.
func TestUpdateSmallNodes(t *testing.T) {
	st := NewStackTrie(nil)
	nt := NewEmpty(NewDatabase(rawdb.NewMemoryDatabase(), nil))
	kvs := []struct {
		K string
		V string
	}{
		{"63303030", "3041"}, // stacktrie.Update
		{"65", "3000"},       // stacktrie.Update
	}
	for _, kv := range kvs {
		nt.Update(common.FromHex(kv.K), common.FromHex(kv.V))
		st.Update(common.FromHex(kv.K), common.FromHex(kv.V))
	}
	if nt.Hash() != st.Hash() {
		t.Fatalf("error %x != %x", st.Hash(), nt.Hash())
	}
}

// TestUpdateVariableKeys contains a case which stacktrie fails: when keys of different
// sizes are used, and the second one has the same prefix as the first, then the
// stacktrie fails, since it's unable to 'expand' on an already added leaf.
// For all practical purposes, this is fine, since keys are fixed-size length
// in account and storage tries.
//
// The test is marked as 'skipped', and exists just to have the behaviour documented.
// This case was found via fuzzing.
func TestUpdateVariableKeys(t *testing.T) {
	t.SkipNow()
	st := NewStackTrie(nil)
	nt := NewEmpty(NewDatabase(rawdb.NewMemoryDatabase(), nil))
	kvs := []struct {
		K string
		V string
	}{
		{"0x33303534636532393561313031676174", "303030"},
		{"0x3330353463653239356131303167617430", "313131"},
	}
	for _, kv := range kvs {
		nt.Update(common.FromHex(kv.K), common.FromHex(kv.V))
		st.Update(common.FromHex(kv.K), common.FromHex(kv.V))
	}
	if nt.Hash() != st.Hash() {
		t.Fatalf("error %x != %x", st.Hash(), nt.Hash())
	}
}

// TestStacktrieNotModifyValues checks that inserting blobs of data into the
// stacktrie does not mutate the blobs
func TestStacktrieNotModifyValues(t *testing.T) {
	st := NewStackTrie(nil)
	{ // Test a very small trie
		// Give it the value as a slice with large backing alloc,
		// so if the stacktrie tries to append, it won't have to realloc
		value := make([]byte, 1, 100)
		value[0] = 0x2
		want := common.CopyBytes(value)
		st.Update([]byte{0x01}, value)
		st.Hash()
		if have := value; !bytes.Equal(have, want) {
			t.Fatalf("tiny trie: have %#x want %#x", have, want)
		}
		st = NewStackTrie(nil)
	}
	// Test with a larger trie
	keyB := big.NewInt(1)
	keyDelta := big.NewInt(1)
	var vals [][]byte
	getValue := func(i int) []byte {
		if i%2 == 0 { // large
			return crypto.Keccak512(big.NewInt(int64(i)).Bytes())
		} else { //small
			return big.NewInt(int64(i)).Bytes()
		}
	}
	for i := range 1000 {
		key := common.BigToHash512(keyB)
		value := getValue(i)
		st.Update(key.Bytes(), value)
		vals = append(vals, value)
		keyB = keyB.Add(keyB, keyDelta)
		keyDelta.Add(keyDelta, common.Big1)
	}
	st.Hash()
	for i := range 1000 {
		want := getValue(i)

		have := vals[i]
		if !bytes.Equal(have, want) {
			t.Fatalf("item %d, have %#x want %#x", i, have, want)
		}
	}
}

// TestStacktrieSerialization tests that the stacktrie works well if we
// serialize/unserialize it a lot
func TestStacktrieSerialization(t *testing.T) {
	var (
		st       = NewStackTrie(nil)
		nt       = NewEmpty(NewDatabase(rawdb.NewMemoryDatabase(), nil))
		keyB     = big.NewInt(1)
		keyDelta = big.NewInt(1)
		vals     [][]byte
		keys     [][]byte
	)
	getValue := func(i int) []byte {
		if i%2 == 0 { // large
			return crypto.Keccak512(big.NewInt(int64(i)).Bytes())
		} else { //small
			return big.NewInt(int64(i)).Bytes()
		}
	}
	for i := range 10 {
		vals = append(vals, getValue(i))
		keys = append(keys, common.BigToHash512(keyB).Bytes())
		keyB = keyB.Add(keyB, keyDelta)
		keyDelta.Add(keyDelta, common.Big1)
	}
	for i, k := range keys {
		nt.Update(k, common.CopyBytes(vals[i]))
	}

	for i, k := range keys {
		blob, err := st.MarshalBinary()
		if err != nil {
			t.Fatal(err)
		}
		newSt, err := NewFromBinary(blob, nil)
		if err != nil {
			t.Fatal(err)
		}
		st = newSt
		st.Update(k, common.CopyBytes(vals[i]))
	}
	if have, want := st.Hash(), nt.Hash(); have != want {
		t.Fatalf("have %#x want %#x", have, want)
	}
}
